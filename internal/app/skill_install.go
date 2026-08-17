package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var bundledSkillFiles = []string{
	"SKILL.md",
	filepath.Join("agents", "openai.yaml"),
}

type skillBundleManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

func verifySkillBundle(root string, requireManifest bool) error {
	payload, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil || !strings.HasPrefix(string(payload), "---\nname: v-local-cli\n") {
		return errors.New("Skill 入口无效")
	}
	manifestPayload, err := os.ReadFile(filepath.Join(root, "skill-manifest.json"))
	if os.IsNotExist(err) && !requireManifest {
		return nil
	}
	if err != nil {
		return errors.New("Skill 清单缺失")
	}
	var manifest skillBundleManifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestPayload)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || manifest.SchemaVersion != 1 || len(manifest.Files) == 0 {
		return errors.New("Skill 清单无效")
	}
	required := append([]string(nil), bundledSkillFiles...)
	referenceEntries, err := os.ReadDir(filepath.Join(root, "references"))
	if err != nil {
		return errors.New("Skill references 缺失")
	}
	for _, entry := range referenceEntries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			required = append(required, filepath.Join("references", entry.Name()))
		}
	}
	for _, relative := range required {
		if _, found := manifest.Files[filepath.ToSlash(relative)]; !found {
			return errors.New("Skill 清单未覆盖全部安装资源")
		}
	}
	if len(manifest.Files) != len(required) {
		return errors.New("Skill 清单包含未授权的额外资源")
	}
	for relative, expected := range manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(relative))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return errors.New("Skill 清单路径越界")
		}
		filePayload, err := os.ReadFile(filepath.Join(root, clean))
		if err != nil || len(filePayload) > 8*1024*1024 {
			return errors.New("Skill 资源不可读")
		}
		digest := sha256.Sum256(filePayload)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), expected) {
			return errors.New("Skill 资源摘要不匹配")
		}
	}
	return nil
}

func findBundledSkill() (string, error) {
	type candidate struct {
		path            string
		requireManifest bool
	}
	candidates := []candidate{}
	if configured := strings.TrimSpace(os.Getenv("V_LOCAL_CLI_SKILL_DIR")); configured != "" {
		candidates = append(candidates, candidate{path: configured, requireManifest: true})
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, candidate{path: filepath.Join(filepath.Dir(executable), "skill"), requireManifest: true})
	}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate.path)
		if err != nil {
			continue
		}
		if verifySkillBundle(absolute, candidate.requireManifest) == nil {
			return absolute, nil
		}
	}
	return "", &commandError{
		typeName: "skill_bundle_unavailable", message: "当前安装包中没有可验证的 v-local-cli Agent Skill",
		hint: "请使用官方 npm 包运行 npx @zanescope/v-local-cli install。", code: 4,
	}
}

func skillInstallTargets() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	agentSkillHome := filepath.Join(home, ".agents", "skills")
	if configured := strings.TrimSpace(os.Getenv("V_LOCAL_CLI_AGENT_SKILL_HOME")); configured != "" {
		agentSkillHome = configured
	}
	targets := []string{filepath.Join(agentSkillHome, "v-local-cli")}
	if configured := strings.TrimSpace(os.Getenv("V_LOCAL_CLI_SKILL_HOME")); configured != "" {
		targets = append(targets, filepath.Join(configured, "v-local-cli"))
	} else if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		targets = append(targets, filepath.Join(codexHome, "skills", "v-local-cli"))
	} else {
		targets = append(targets, filepath.Join(home, ".codex", "skills", "v-local-cli"))
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(targets))
	for _, target := range targets {
		absolute, err := filepath.Abs(target)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(filepath.Clean(absolute))
		if !seen[key] {
			seen[key] = true
			unique = append(unique, absolute)
		}
	}
	return unique, nil
}

func copySkillFile(sourceRoot, destinationRoot, relative string) error {
	source := filepath.Join(sourceRoot, relative)
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Skill 资源不是普通文件")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	target := filepath.Join(destinationRoot, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, 8*1024*1024+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if written > 8*1024*1024 {
		return errors.New("Skill 资源超过安全大小上限")
	}
	return closeErr
}

func copyBundledSkill(sourceRoot, target string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".v-local-cli-skill-*.tmp")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, relative := range bundledSkillFiles {
		if err := copySkillFile(sourceRoot, stage, relative); err != nil {
			return err
		}
	}
	references := filepath.Join(sourceRoot, "references")
	entries, err := os.ReadDir(references)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		if err := copySkillFile(sourceRoot, stage, filepath.Join("references", entry.Name())); err != nil {
			return err
		}
	}
	backup := fmt.Sprintf("%s.old.%d", target, time.Now().UnixNano())
	movedOld := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		movedOld = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		if movedOld {
			_ = os.Rename(backup, target)
		}
		return err
	}
	published = true
	if movedOld {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func installBundledSkill(showPaths bool) ([]string, error) {
	source, err := findBundledSkill()
	if err != nil {
		return nil, err
	}
	targets, err := skillInstallTargets()
	if err != nil {
		return nil, &commandError{typeName: "skill_install_failed", message: "无法确定 Agent Skill 安装目录", code: 4}
	}
	actions := make([]string, 0, len(targets))
	for _, target := range targets {
		if err := copyBundledSkill(source, target); err != nil {
			return nil, &commandError{typeName: "skill_install_failed", message: "Agent Skill 安装失败", hint: "检查当前用户对 Agent Skill 目录的写入权限。", code: 4}
		}
		label := "已安装 v-local-cli Agent Skill"
		if showPaths {
			label += "：" + target
		}
		actions = append(actions, label)
	}
	return actions, nil
}
