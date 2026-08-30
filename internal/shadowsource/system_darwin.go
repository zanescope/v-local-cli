//go:build darwin

package shadowsource

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

type boundedOutput struct {
	data []byte
	over bool
}

func (value *boundedOutput) Write(payload []byte) (int, error) {
	remaining := 32*1024 - len(value.data)
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		value.data = append(value.data, payload[:remaining]...)
	}
	if remaining < len(payload) {
		value.over = true
	}
	return len(payload), nil
}

func systemCommand(ctx context.Context, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Env = []string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"}
	output := &boundedOutput{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil || output.over {
		return "", errors.New("bounded source qualification command failed")
	}
	return strings.TrimSpace(string(output.data)), nil
}

func systemVerifyStrict(ctx context.Context, sourcePath string) error {
	_, err := systemCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", sourcePath)
	return err
}

func lineValue(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func systemCodeIdentity(ctx context.Context, sourcePath string) (CodeIdentity, error) {
	identityOutput, err := systemCommand(ctx, "/usr/bin/codesign", "-d", "--verbose=4", sourcePath)
	if err != nil {
		return CodeIdentity{}, err
	}
	requirementOutput, err := systemCommand(ctx, "/usr/bin/codesign", "-d", "-r-", sourcePath)
	if err != nil {
		return CodeIdentity{}, err
	}
	identifier := lineValue(identityOutput, "Identifier=")
	team := lineValue(identityOutput, "TeamIdentifier=")
	requirement := lineValue(requirementOutput, "designated =>")
	if identifier == "" || team == "" || requirement == "" {
		return CodeIdentity{}, errors.New("source code identity output is incomplete")
	}
	return CodeIdentity{Identifier: identifier, Team: team, Requirement: requirement}, nil
}

func systemPlistString(ctx context.Context, plistPath, key string) (string, error) {
	return systemCommand(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plistPath)
}
