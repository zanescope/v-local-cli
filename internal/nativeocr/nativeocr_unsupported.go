//go:build !windows || !amd64

package nativeocr

import (
	"context"
	"runtime"
)

func Current(bool) Status {
	return Status{
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, Source: "installed_wechat",
		ExternalDependency: false, PrivateIPC: true, NetworkRequested: false, Reason: "仅 Windows amd64 支持实验性微信原生 OCR",
	}
}

func Recognize(context.Context, string) (Result, error) { return Result{}, ErrUnsupported }
