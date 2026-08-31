//go:build !darwin

package app

import "context"

// Persistent Shadow publication is unavailable off macOS. The synthetic
// Owner entry supplies its own in-memory startup gate and never reports a
// production account ready.
func reconcilePlatformShadowGenerations(context.Context) error { return nil }
