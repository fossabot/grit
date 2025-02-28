# Checkpoint/Restore Fine-Tuning Job

## Introduction

Checkpoint and Restore a pytorch based model fine-tuning job on a GPU node using CRIU tool.

## Experiment Env

Node: Standard NC6s v3 VM with NVIDIA Tesla V100 GPU.

OS: Ubuntu 24.04

## Steps

### 1. Upgrade to NVIDIA driver 570

cuda-checkpoint pytorch supports was introduced in NVIDIA driver 570. Installed the latest NVIDIA driver following this [instruction](https://learn.microsoft.com/en-us/azure/virtual-machines/linux/n-series-driver-setup#ubuntu).

After installation, make sure the driver version is 570, CUDA version is 12.8.

```bash
root@nvcr:/home/qzhuang# nvidia-smi
Mon Feb 24 09:44:45 2025       
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 570.86.15              Driver Version: 570.86.15      CUDA Version: 12.8     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  Tesla V100-PCIE-16GB           Off |   00000001:00:00.0 Off |                  Off |
| N/A   29C    P0             24W /  250W |       1MiB /  16384MiB |      0%      Default |
|                                         |                        |                  N/A |
+-----------------------------------------+------------------------+----------------------+
                                                                                         
+-----------------------------------------------------------------------------------------+
| Processes:                                                                              |
|  GPU   GI   CI              PID   Type   Process name                        GPU Memory |
|        ID   ID                                                               Usage      |
|=========================================================================================|
|  No running processes found                                                             |
+-----------------------------------------------------------------------------------------+
```

Hint: If the machine fails to boot after the driver installation, try disable `Secure Boot` and try again.


### 2. Compile latest CRIU

Requires latest CRIU changes to support pytorch checkpoint/restore.

1. Build criu and cuda_plugin.so

```bash
git clone https://github.com/checkpoint-restore/criu

cd criu
make docker-build

root@nvcr:/home/qzhuang# docker images
REPOSITORY                                    TAG       IMAGE ID       CREATED        SIZE
criu-x86_64                                   latest    fda931b47ea8   23 hours ago   780MB
```

2. Install criu

```bash
root@nvcr:/home/qzhuang# mkdir criuinstall
root@nvcr:/home/qzhuang# docker run --name criu -v ./criuinstall:/install:rw -it criu-x86_64 bash
root@1ef8c8e589cc:/criu# apt install asciidoctor -y
root@1ef8c8e589cc:/criu# make DESTDIR=/install install
root@nvcr:/home/qzhuang# cp -r ./criuinstall/* /
```

3. Verify install 

```bash
root@nvcr:/home/qzhuang# criu -V
Version: 4.0
GitID: v4.0-67-gb0f0d0fa0
root@nvcr:/home/qzhuang# criu check
Looks good.
```

### 3. Run fine-tuning job and make checkpoint

1. Start a fine-tuning job

```bash
# export DATASET_FOLDER_PATH=/root/workspace/data/
(myenv) root@nvcr:~/kaito/presets/workspace/tuning/text-generation# accelerate launch --num_processes=1 ./fine_tuning.py

...

  return fn(*args, **kwargs)
/root/demo/myenv/lib/python3.12/site-packages/torch/utils/checkpoint.py:295: FutureWarning: `torch.cpu.amp.autocast(args...)` is deprecated. Please use `torch.amp.autocast('cpu', args...)` instead.
  with torch.enable_grad(), device_autocast_ctx, torch.cpu.amp.autocast(**ctx.cpu_autocast_kwargs):  # type: ignore[attr-defined]
  7%|███████▎                                                                                                 | 14/200 [00:11<02:06,  1.47it/s]
```

Pause this job for checkpoint. We can see the fine-tuning job run to step 14.

```bash
root@nvcr:~# nvidia-smi
Mon Feb 24 10:21:17 2025       
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 570.86.15              Driver Version: 570.86.15      CUDA Version: 12.8     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  Tesla V100-PCIE-16GB           Off |   00000001:00:00.0 Off |                  Off |
| N/A   35C    P0            175W /  250W |    5508MiB /  16384MiB |     93%      Default |
|                                         |                        |                  N/A |
+-----------------------------------------+------------------------+----------------------+
                                                                                         
+-----------------------------------------------------------------------------------------+
| Processes:                                                                              |
|  GPU   GI   CI              PID   Type   Process name                        GPU Memory |
|        ID   ID                                                               Usage      |
|=========================================================================================|
|    0   N/A  N/A           89102      C   /root/demo/myenv/bin/python3           5504MiB |
+-----------------------------------------------------------------------------------------+
root@nvcr:~# export PID=89102
root@nvcr:~# cuda-checkpoint --toggle --pid $PID
# Ensure process is not consuming GPU
root@nvcr:~# nvidia-smi --query --display=PIDS | grep $PID
```

2. Make checkpoint

```bash
root@nvcr:~# mkdir images
root@nvcr:~# criu dump --shell-job --images-dir ./images --tree $PID  --tcp-established
# add --leave-running option to make criu not kill the tasks after dump. 
root@nvcr:~# du -sh images/
7.2G	images/
# The size of the CRIU image is related to GPU memory usage.
```

### 4. Restore the job from checkpoint

```bash
root@nvcr:~# criu restore --shell-job --restore-detached --images-dir images --tcp-established
# toggle the process to let it continue to run.
root@nvcr:~# cuda-checkpoint --toggle --pid $PID
 12%|████████████▌                                                                                            | 24/200 [08:47<20:38,  7.04s/it]
# it will continue to run from the step 15 to the end successfully.
root@nvcr:~# nvidia-smi
Mon Feb 24 10:31:09 2025       
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 570.86.15              Driver Version: 570.86.15      CUDA Version: 12.8     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  Tesla V100-PCIE-16GB           Off |   00000001:00:00.0 Off |                  Off |
| N/A   54C    P0            205W /  250W |    5568MiB /  16384MiB |     96%      Default |
|                                         |                        |                  N/A |
+-----------------------------------------+------------------------+----------------------+
                                                                                         
+-----------------------------------------------------------------------------------------+
| Processes:                                                                              |
|  GPU   GI   CI              PID   Type   Process name                        GPU Memory |
|        ID   ID                                                               Usage      |
|=========================================================================================|
|    0   N/A  N/A           89102      C   /root/demo/myenv/bin/python3           5564MiB |
+-----------------------------------------------------------------------------------------+
```
