// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.
package options

import (
	"time"

	"github.com/spf13/pflag"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/component-base/config/options"
)

type GritManagerOptions struct {
	Version            bool
	WebhookPort        int
	MetricsPort        int
	HealthProbePort    int
	EnableProfiling    bool
	LeaderElection     componentbaseconfig.LeaderElectionConfiguration
	KubeClientQPS      int
	KubeClientBurst    int
	WorkingNamespace   string
	WebhookSecretName  string
	WebhookServiceName string
	ExpirationDuration time.Duration
}

func NewGritManagerOptions() *GritManagerOptions {
	return &GritManagerOptions{
		Version:         false,
		WebhookPort:     10350,
		MetricsPort:     10351,
		HealthProbePort: 10352,
		EnableProfiling: true,
		LeaderElection: componentbaseconfig.LeaderElectionConfiguration{
			LeaderElect:       true,
			ResourceLock:      resourcelock.LeasesResourceLock,
			ResourceName:      "grit-manager",
			ResourceNamespace: "kaito-workspace",
		},
		KubeClientQPS:      50,
		KubeClientBurst:    100,
		WorkingNamespace:   "kaito-workspace",
		WebhookSecretName:  "grit-manager-webhook-certs",
		WebhookServiceName: "grit-manager-webhook-svc",
		ExpirationDuration: 10 * 364 * 24 * time.Hour, // 10 years
	}
}

func (o *GritManagerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&o.Version, "version", o.Version, "print the version information, and then exit")
	fs.IntVar(&o.WebhookPort, "webhook-port", o.WebhookPort, "the port the webhook endpoint binds to for validating and mutating resources.")
	fs.IntVar(&o.MetricsPort, "metrics-port", o.MetricsPort, "the port the metric endpoint binds to for serving metrics about grit-manager.")
	fs.IntVar(&o.HealthProbePort, "health-probe-port", o.HealthProbePort, "the port the health probe endpoint binds to for serving livness check.")
	fs.BoolVar(&o.EnableProfiling, "enable-profiling", o.EnableProfiling, "enable the profiling on the metric endpoint.")
	options.BindLeaderElectionFlags(&o.LeaderElection, fs)
	fs.IntVar(&o.KubeClientQPS, "kube-client-qps", o.KubeClientQPS, "the rate of qps to kube-apiserver.")
	fs.IntVar(&o.KubeClientBurst, "kube-client-burst", o.KubeClientBurst, "the max allowed burst of queries to the kube-apiserver.")
	fs.StringVar(&o.WorkingNamespace, "working-namespace", o.WorkingNamespace, "the namespace where the grit-manager is working.")
	fs.StringVar(&o.WebhookSecretName, "webhook-secret-name", o.WebhookSecretName, "the secret which used for storing certificates for grit webhook")
	fs.StringVar(&o.WebhookServiceName, "webhook-service-name", o.WebhookServiceName, "the service which used for accessing grit webhook")
	fs.DurationVar(&o.ExpirationDuration, "cert-duration", o.ExpirationDuration, "the expiration duration of webhook server certificates")
}
