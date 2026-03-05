package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

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

	if err := yaml.Unmarshal([]byte(frontMatter), config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}

	resolveEnvReferences(config)

	if !terminated {
		return config, fmt.Errorf("%w: %s", ErrUnterminatedFrontMatter, path)
	}

	return config, nil
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

	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		resolveEnvReferencesValue(v.Elem())
		return
	}

	if v.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if !field.CanSet() {
			continue
		}

		fieldType := v.Type().Field(i)

		if fieldType.Name == "PromptTemplate" {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			resolved, ok := resolveEnvToken(field.String())
			if ok {
				field.SetString(resolved)
			}
		case reflect.Struct, reflect.Pointer:
			resolveEnvReferencesValue(field)
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

	return os.Getenv(name), true
}
