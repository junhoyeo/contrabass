package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"
	"gopkg.in/yaml.v3"
)

var envVarPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	ErrUnterminatedFrontMatter = errors.New("unterminated front matter")
	ErrInvalidYAML             = errors.New("invalid workflow yaml")
	ErrFrontMatterNotMap       = errors.New("workflow front matter must be a map")
)

func ParseWorkflow(path string) (*WorkflowConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	frontMatter, promptTemplate, hasFrontMatter, terminated := splitFrontMatter(string(content))

	config := &WorkflowConfig{PromptTemplate: strings.TrimSpace(promptTemplate)}

	if !hasFrontMatter {
		return config, nil
	}

	if strings.TrimSpace(frontMatter) == "" {
		if !terminated {
			return config, fmt.Errorf("%w: %s", ErrUnterminatedFrontMatter, path)
		}
		return config, nil
	}

	var parsed any
	if err := yaml.Unmarshal([]byte(frontMatter), &parsed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}

	if !isYAMLMap(parsed) {
		return nil, ErrFrontMatterNotMap
	}

	decoder := yaml.NewDecoder(strings.NewReader(frontMatter))
	decoder.KnownFields(true)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	if err := ensureSingleYAMLDocument(decoder); err != nil {
		return nil, err
	}
	config.presentFields = collectPresentFields(parsed)

	resolveEnvReferences(config)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if !terminated {
		return config, fmt.Errorf("%w: %s", ErrUnterminatedFrontMatter, path)
	}

	return config, nil
}

func ensureSingleYAMLDocument(decoder *yaml.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}
	return fmt.Errorf("%w: multiple YAML documents are not supported", ErrInvalidYAML)
}

func validateLinearConfig(cfg *WorkflowConfig) error {
	if cfg == nil || !cfg.LinearSyncCommentsEnabled() {
		return nil
	}
	switch strings.TrimSpace(strings.ToLower(cfg.Linear.SyncComments.Mode)) {
	case "", LinearSyncCommentsModeReplyThread, LinearSyncCommentsModeTopLevel:
		return nil
	default:
		return fmt.Errorf("%w: valid values: reply_thread, top_level", ErrLinearSyncCommentsModeInvalid)
	}
}

func collectPresentFields(value any) map[string]struct{} {
	present := make(map[string]struct{})
	var walk func(any, string)
	walk = func(current any, prefix string) {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				path := key
				if prefix != "" {
					path = prefix + "." + key
				}
				present[path] = struct{}{}
				walk(nested, path)
			}
		case map[any]any:
			for key, nested := range typed {
				keyString, ok := key.(string)
				if !ok {
					continue
				}
				path := keyString
				if prefix != "" {
					path = prefix + "." + keyString
				}
				present[path] = struct{}{}
				walk(nested, path)
			}
		}
	}
	walk(value, "")
	return present
}

func splitFrontMatter(content string) (frontMatter string, prompt string, hasFrontMatter bool, terminated bool) {
	if !strings.HasPrefix(content, "---") {
		return "", content, false, false
	}

	if len(content) > 3 {
		next := content[3]
		if next != '\n' && next != '\r' {
			return "", content, false, false
		}
	}

	startOffset := 4
	if strings.HasPrefix(content, "---\r\n") {
		startOffset = 5
	}

	// Guard against panic on minimal front matter input (e.g., "---", "---\n")
	// Treat as empty, terminated front matter block
	if len(content) <= startOffset {
		return "", "", true, true
	}

	remainder := content[startOffset:]
	lines := strings.SplitAfter(remainder, "\n")
	var yamlBuilder strings.Builder
	offset := 0

	for _, line := range lines {
		offset += len(line)
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "---" {
			return yamlBuilder.String(), remainder[offset:], true, true
		}
		yamlBuilder.WriteString(line)
	}

	return yamlBuilder.String(), "", true, false
}

func isYAMLMap(v any) bool {
	switch v.(type) {
	case map[string]any, map[any]any:
		return true
	default:
		return false
	}
}

func resolveEnvReferences(cfg *WorkflowConfig) {
	if cfg == nil {
		return
	}

	resolveEnvReferencesValue(reflect.ValueOf(cfg).Elem())
}

func resolveEnvReferencesValue(v reflect.Value) {
	if !v.IsValid() {
		return
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		resolved := reflect.New(v.Elem().Type()).Elem()
		resolved.Set(v.Elem())
		resolveEnvReferencesValue(resolved)
		v.Set(resolved)
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		resolveEnvReferencesValue(v.Elem())
	case reflect.String:
		if v.CanSet() {
			resolved, ok := resolveEnvToken(v.String())
			if ok {
				v.SetString(resolved)
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanSet() {
				continue
			}
			if v.Type().Field(i).Name == "PromptTemplate" {
				continue
			}
			resolveEnvReferencesValue(field)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			resolveEnvReferencesValue(v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			value := reflect.New(iter.Value().Type()).Elem()
			value.Set(iter.Value())
			resolveEnvReferencesValue(value)
			v.SetMapIndex(iter.Key(), value)
		}
	}
}

func resolveEnvToken(value string) (string, bool) {
	if !strings.HasPrefix(value, "$") {
		return "", false
	}

	name := strings.TrimPrefix(value, "$")
	if !envVarPattern.MatchString(name) {
		return "", false
	}

	resolved := os.Getenv(name)
	if resolved == "" {
		log.Debug("environment variable reference resolved empty", "name", name)
	}

	return resolved, true
}
