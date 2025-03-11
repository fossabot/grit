// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package util

import "context"

var (
	controllerNameKey = struct{}{}
)

func WithControllerName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, controllerNameKey, name)
}
