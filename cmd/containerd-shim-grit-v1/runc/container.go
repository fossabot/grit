//go:build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package runc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/console"
	"github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/api/types/runc/options"
	runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/pelletier/go-toml/v2"

	"github.com/kaito-project/grit/cmd/containerd-shim-grit-v1/process"
)

// NewContainer returns a new runc container
func NewContainer(ctx context.Context, platform stdio.Platform, r *task.CreateTaskRequest) (_ *Container, retErr error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, fmt.Errorf("create namespace: %w", err)
	}

	opts, err := readRuntimeOptions(r)
	if err != nil {
		return nil, fmt.Errorf("read runtime options: %w", err)
	}
	log.G(ctx).Debugf("runtime options: %v", opts)

	ckptOpts, err := ReadCheckpointOpts(r.Bundle)
	if ckptOpts != nil {
		checkpointPath := ckptOpts.GetCheckpointPath()
		if _, err := os.Stat(checkpointPath); err == nil {
			r.Checkpoint = checkpointPath
		} else if os.IsNotExist(err) {
			log.G(ctx).Warnf("Checkpoint path %s does not exist, skip restoration", r.Checkpoint)
		} else {
			log.G(ctx).WithError(err).Errorf("Error checking checkpoint path %s", r.Checkpoint)
			return nil, fmt.Errorf("error checking checkpoint path %s: %w", r.Checkpoint, err)
		}
	} else if err != nil {
		log.G(ctx).WithError(err).Error("failed to read checkpoint options")
		return nil, fmt.Errorf("failed to read checkpoint options: %w", err)
	}

	var pmounts []process.Mount
	for _, m := range r.Rootfs {
		pmounts = append(pmounts, process.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Target:  m.Target,
			Options: m.Options,
		})
	}

	rootfs := ""
	if len(pmounts) > 0 {
		rootfs = filepath.Join(r.Bundle, "rootfs")
		if err := os.Mkdir(rootfs, 0711); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}

	config := &process.CreateConfig{
		ID:               r.ID,
		Bundle:           r.Bundle,
		Runtime:          opts.BinaryName,
		Rootfs:           pmounts,
		Terminal:         r.Terminal,
		Stdin:            r.Stdin,
		Stdout:           r.Stdout,
		Stderr:           r.Stderr,
		Checkpoint:       r.Checkpoint,
		ParentCheckpoint: r.ParentCheckpoint,
		Options:          r.Options,
	}

	if err := WriteOptions(r.Bundle, opts); err != nil {
		return nil, err
	}
	// For historical reason, we write opts.BinaryName as well as the entire opts
	if err := WriteRuntime(r.Bundle, opts.BinaryName); err != nil {
		return nil, err
	}

	var mounts []mount.Mount
	for _, pm := range pmounts {
		mounts = append(mounts, mount.Mount{
			Type:    pm.Type,
			Source:  pm.Source,
			Target:  pm.Target,
			Options: pm.Options,
		})
	}
	defer func() {
		if retErr != nil {
			if err := mount.UnmountMounts(mounts, rootfs, 0); err != nil {
				log.G(ctx).WithError(err).Warn("failed to cleanup rootfs mount")
			}
		}
	}()
	if err := mount.All(mounts, rootfs); err != nil {
		return nil, fmt.Errorf("failed to mount rootfs component: %w", err)
	}

	if r.Checkpoint != "" {
		// Unpack rootfs-diff.tar if it exists.
		// This needs to happen before the 'Start()'.
		rootfsDiff := ckptOpts.GetRootFsDiffTar()

		_, err = os.Stat(rootfsDiff)
		if err == nil {
			rootfsDiffTar, err := os.Open(rootfsDiff)
			if err != nil {
				return nil, fmt.Errorf("failed to open rootfs-diff archive %s for import: %w", rootfsDiffTar.Name(), err)
			}
			defer func(f *os.File) {
				if err := f.Close(); err != nil {
					log.G(ctx).Errorf("Unable to close file %s: %q", f.Name(), err)
				}
			}(rootfsDiffTar)

			decompressed, err := compression.DecompressStream(rootfsDiffTar)
			if err != nil {
				return nil, fmt.Errorf("failed to decompress archive %s for import: %w", rootfsDiffTar.Name(), err)
			}

			_, err = archive.Apply(
				ctx,
				rootfs,
				decompressed,
			)

			if err != nil {
				return nil, fmt.Errorf("unpacking of rootfs-diff archive %s into %s failed: %w", rootfsDiffTar.Name(), rootfs, err)
			}
			log.G(ctx).Debugf("Unpacked checkpoint in %s", rootfs)
		}
	}

	p, err := newInit(
		ctx,
		r.Bundle,
		filepath.Join(r.Bundle, "work"),
		ns,
		platform,
		config,
		opts,
		rootfs,
	)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	if err := p.Create(ctx, config); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	container := &Container{
		ID:              r.ID,
		Bundle:          r.Bundle,
		process:         p,
		processes:       make(map[string]process.Process),
		reservedProcess: make(map[string]struct{}),
	}
	pid := p.Pid()
	if pid > 0 {
		if cg, err := loadProcessCgroup(ctx, pid); err == nil {
			container.cgroup = cg
		}
	}
	return container, nil
}

func readRuntimeOptions(r *task.CreateTaskRequest) (*options.Options, error) {
	opts := &options.Options{}
	if r.Options == nil {
		return opts, nil
	}

	v, err := typeurl.UnmarshalAny(r.Options)
	if err != nil {
		return nil, err
	}

	o, ok := v.(*runtimeoptions.Options)
	if !ok {
		return nil, fmt.Errorf("unknown runtime options type %T", v)
	}

	if o.ConfigPath == "" {
		oo, err := typeurl.UnmarshalByTypeURL(o.GetTypeUrl(), o.GetConfigBody())
		if err != nil {
			return nil, err
		}
		if _, ok := oo.(*options.Options); ok {
			return oo.(*options.Options), nil
		}
		return nil, fmt.Errorf("unknown runtime options type %T", oo)
	}

	f, err := os.Open(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := toml.NewDecoder(f).Decode(opts); err != nil {
		return nil, fmt.Errorf("failed to decode runtime options: %w", err)
	}
	return opts, nil
}

const optionsFilename = "options.json"

// ReadOptions reads the option information from the path.
// When the file does not exist, ReadOptions returns nil without an error.
func ReadOptions(path string) (*options.Options, error) {
	filePath := filepath.Join(path, optionsFilename)
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var opts options.Options
	if err := json.Unmarshal(data, &opts); err != nil {
		return nil, err
	}
	return &opts, nil
}

// WriteOptions writes the options information into the path
func WriteOptions(path string, opts *options.Options) error {
	data, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, optionsFilename), data, 0600)
}

// ReadRuntime reads the runtime information from the path
func ReadRuntime(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(path, "runtime"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteRuntime writes the runtime information into the path
func WriteRuntime(path, runtime string) error {
	return os.WriteFile(filepath.Join(path, "runtime"), []byte(runtime), 0600)
}

func newInit(ctx context.Context, path, workDir, namespace string, platform stdio.Platform,
	r *process.CreateConfig, options *options.Options, rootfs string) (*process.Init, error) {
	runtime := process.NewRunc(options.Root, path, namespace, options.BinaryName, options.SystemdCgroup)
	p := process.New(r.ID, runtime, stdio.Stdio{
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Terminal: r.Terminal,
	})
	p.Bundle = r.Bundle
	p.Platform = platform
	p.Rootfs = rootfs
	p.WorkDir = workDir
	p.IoUID = int(options.IoUid)
	p.IoGID = int(options.IoGid)
	p.NoPivotRoot = options.NoPivotRoot
	p.NoNewKeyring = options.NoNewKeyring
	p.CriuWorkPath = options.CriuWorkPath
	if p.CriuWorkPath == "" {
		// if criu work path not set, use container WorkDir
		p.CriuWorkPath = p.WorkDir
	}
	return p, nil
}

// Container for operating on a runc container and its processes
type Container struct {
	mu sync.Mutex

	// ID of the container
	ID string
	// Bundle path
	Bundle string

	// cgroup is either cgroups.Cgroup or *cgroupsv2.Manager
	cgroup          interface{}
	process         process.Process
	processes       map[string]process.Process
	reservedProcess map[string]struct{}
}

// All processes in the container
func (c *Container) All() (o []process.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range c.processes {
		o = append(o, p)
	}
	if c.process != nil {
		o = append(o, c.process)
	}
	return o
}

// ExecdProcesses added to the container
func (c *Container) ExecdProcesses() (o []process.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.processes {
		o = append(o, p)
	}
	return o
}

// Pid of the main process of a container
func (c *Container) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process.Pid()
}

// Cgroup of the container
func (c *Container) Cgroup() interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cgroup
}

// CgroupSet sets the cgroup to the container
func (c *Container) CgroupSet(cg interface{}) {
	c.mu.Lock()
	c.cgroup = cg
	c.mu.Unlock()
}

// Process returns the process by id
func (c *Container) Process(id string) (process.Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		if c.process == nil {
			return nil, fmt.Errorf("container must be created: %w", errdefs.ErrFailedPrecondition)
		}
		return c.process, nil
	}
	p, ok := c.processes[id]
	if !ok {
		return nil, fmt.Errorf("process does not exist %s: %w", id, errdefs.ErrNotFound)
	}
	return p, nil
}

// ReserveProcess checks for the existence of an id and atomically
// reserves the process id if it does not already exist
//
// Returns true if the process id was successfully reserved and a
// cancel func to release the reservation
func (c *Container) ReserveProcess(id string) (bool, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.processes[id]; ok {
		return false, nil
	}
	if _, ok := c.reservedProcess[id]; ok {
		return false, nil
	}
	c.reservedProcess[id] = struct{}{}
	return true, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.reservedProcess, id)
	}
}

// ProcessAdd adds a new process to the container
func (c *Container) ProcessAdd(process process.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.reservedProcess, process.ID())
	c.processes[process.ID()] = process
}

// ProcessRemove removes the process by id from the container
func (c *Container) ProcessRemove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.processes, id)
}

// Start a container process
func (c *Container) Start(ctx context.Context, r *task.StartRequest) (process.Process, error) {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return nil, err
	}
	if err := p.Start(ctx); err != nil {
		return p, err
	}
	if c.Cgroup() == nil && p.Pid() > 0 {
		if cg, err := loadProcessCgroup(ctx, p.Pid()); err == nil {
			c.cgroup = cg
		}
	}
	return p, nil
}

// Delete the container or a process by id
func (c *Container) Delete(ctx context.Context, r *task.DeleteRequest) (process.Process, error) {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return nil, err
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	if r.ExecID != "" {
		c.ProcessRemove(r.ExecID)
	}
	return p, nil
}

// Exec an additional process
func (c *Container) Exec(ctx context.Context, r *task.ExecProcessRequest) (process.Process, error) {
	process, err := c.process.(*process.Init).Exec(ctx, c.Bundle, &process.ExecConfig{
		ID:       r.ExecID,
		Terminal: r.Terminal,
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Spec:     r.Spec,
	})
	if err != nil {
		return nil, err
	}
	c.ProcessAdd(process)
	return process, nil
}

// Pause the container
func (c *Container) Pause(ctx context.Context) error {
	return c.process.(*process.Init).Pause(ctx)
}

// Resume the container
func (c *Container) Resume(ctx context.Context) error {
	return c.process.(*process.Init).Resume(ctx)
}

// ResizePty of a process
func (c *Container) ResizePty(ctx context.Context, r *task.ResizePtyRequest) error {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return err
	}
	ws := console.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	return p.Resize(ws)
}

// Kill a process
func (c *Container) Kill(ctx context.Context, r *task.KillRequest) error {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return err
	}
	return p.Kill(ctx, r.Signal, r.All)
}

// CloseIO of a process
func (c *Container) CloseIO(ctx context.Context, r *task.CloseIORequest) error {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return err
	}
	if stdin := p.Stdin(); stdin != nil {
		if err := stdin.Close(); err != nil {
			return fmt.Errorf("close stdin: %w", err)
		}
	}
	return nil
}

// Checkpoint the container
func (c *Container) Checkpoint(ctx context.Context, r *task.CheckpointTaskRequest) error {
	p, err := c.Process("")
	if err != nil {
		return err
	}

	var opts options.CheckpointOptions
	if r.Options != nil {
		if err := typeurl.UnmarshalTo(r.Options, &opts); err != nil {
			return err
		}
	}
	return p.(*process.Init).Checkpoint(ctx, &process.CheckpointConfig{
		Path:                     r.Path,
		Exit:                     opts.Exit,
		AllowOpenTCP:             opts.OpenTcp,
		AllowExternalUnixSockets: opts.ExternalUnixSockets,
		AllowTerminal:            opts.Terminal,
		FileLocks:                opts.FileLocks,
		EmptyNamespaces:          opts.EmptyNamespaces,
		WorkDir:                  opts.WorkPath,
	})
}

// Update the resource information of a running container
func (c *Container) Update(ctx context.Context, r *task.UpdateTaskRequest) error {
	p, err := c.Process("")
	if err != nil {
		return err
	}
	return p.(*process.Init).Update(ctx, r.Resources)
}

// HasPid returns true if the container owns a specific pid
func (c *Container) HasPid(pid int) bool {
	if c.Pid() == pid {
		return true
	}
	for _, p := range c.All() {
		if p.Pid() == pid {
			return true
		}
	}
	return false
}

func loadProcessCgroup(ctx context.Context, pid int) (cg interface{}, err error) {
	if cgroups.Mode() == cgroups.Unified {
		g, err := cgroupsv2.PidGroupPath(pid)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("loading cgroup2 for %d", pid)
			return nil, err
		}
		cg, err = cgroupsv2.Load(g)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("loading cgroup2 for %d", pid)
			return nil, err
		}
	} else {
		cg, err = cgroup1.Load(cgroup1.PidPath(pid))
		if err != nil {
			log.G(ctx).WithError(err).Errorf("loading cgroup for %d", pid)
			return nil, err
		}
	}
	return cg, nil
}
