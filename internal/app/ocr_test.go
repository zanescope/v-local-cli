package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/zanescope/v-local-cli/internal/nativeocr"
)

func TestRecognizeTemporaryChatImageCleansPlaintextAfterRecognitionFailure(t *testing.T) {
	previous := recognizeNativeOCR
	defer func() { recognizeNativeOCR = previous }()
	recognizeNativeOCR = func(context.Context, string) (nativeocr.Result, error) {
		return nativeocr.Result{}, errors.New("injected recognition failure")
	}
	directory := t.TempDir()
	_, invoked, operationErr, cleanupErr := recognizeTemporaryChatImage(context.Background(), directory, "png", []byte("image plaintext"))
	if !invoked || operationErr == nil {
		t.Fatalf("recognition failure was not injected: invoked=%v err=%v", invoked, operationErr)
	}
	if cleanupErr != nil {
		t.Fatalf("temporary image cleanup failed: %v", cleanupErr)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary image remained after recognition failure: %v", entries)
	}
}
