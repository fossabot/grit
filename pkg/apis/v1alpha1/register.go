// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group = "kaito.sh"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: "v1alpha1"}
	SchemeBuilder      = runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&Checkpoint{},
			&CheckpointList{},
			&Restore{},
			&RestoreList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	})
)
