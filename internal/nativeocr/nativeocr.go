package nativeocr

import "errors"

var ErrUnsupported = errors.New("当前平台或微信版本不支持原生 OCR")

type Status struct {
	Available           bool   `json:"backend_ready"`
	Platform            string `json:"platform"`
	Architecture        string `json:"architecture"`
	WeChatVersion       string `json:"wechat_version,omitempty"`
	Source              string `json:"source"`
	ExternalDependency  bool   `json:"external_dependency"`
	PrivateIPC          bool   `json:"private_ipc"`
	NetworkRequested    bool   `json:"network_requested_by_cli"`
	SubprocessSandboxed bool   `json:"subprocess_sandboxed"`
	VendorNoSandbox     bool   `json:"vendor_no_sandbox_switch"`
	Reason              string `json:"reason,omitempty"`
	WeChatPath          string `json:"wechat_path,omitempty"`
}

type Line struct {
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence,omitempty"`
	Left       float32 `json:"left,omitempty"`
	Top        float32 `json:"top,omitempty"`
	Right      float32 `json:"right,omitempty"`
	Bottom     float32 `json:"bottom,omitempty"`
}

type Result struct {
	Width                 uint32 `json:"width,omitempty"`
	Height                uint32 `json:"height,omitempty"`
	Lines                 []Line `json:"lines"`
	Text                  string `json:"text"`
	Backend               string `json:"backend"`
	WeChatVersion         string `json:"wechat_version,omitempty"`
	PrivateIPCInvoked     bool   `json:"private_ipc_invoked"`
	NetworkRequested      bool   `json:"network_requested_by_cli"`
	TemporaryFilesRemoved bool   `json:"temporary_files_removed"`
}
