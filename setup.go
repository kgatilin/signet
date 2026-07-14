package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Setup struct {
	EnvFiles   []string      `yaml:"envFiles"`
	Files      []SetupFile   `yaml:"files"`
	RequireEnv []string      `yaml:"requireEnv"`
	Build      BuildCommands `yaml:"build"`
	Services   []Service     `yaml:"services"`
}

type SetupFile struct {
	Name    string `yaml:"name"`
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// BuildCommands is a list of shell commands run to completion before any case.
// It accepts either a single string or a list of strings in YAML.
type BuildCommands []string

func (b *BuildCommands) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var single string
		if err := node.Decode(&single); err != nil {
			return err
		}
		*b = BuildCommands{single}
	case yaml.SequenceNode:
		var many []string
		if err := node.Decode(&many); err != nil {
			return err
		}
		*b = BuildCommands(many)
	default:
		return fmt.Errorf("setup.build must be a string or a list of strings")
	}
	return nil
}

// Service is a long-running background process started before cases, kept alive
// while they run, and stopped afterwards.
type Service struct {
	Name        string   `yaml:"name"`
	Shell       string   `yaml:"shell"`
	Args        []string `yaml:"args"`
	Ready       Ready    `yaml:"ready"`
	StopTimeout string   `yaml:"stopTimeout"`
}

// Ready describes how signet decides a service is up before running cases.
type Ready struct {
	Shell   string `yaml:"shell"`
	Log     string `yaml:"log"`
	Timeout string `yaml:"timeout"`
}

type setupContext struct {
	dir      string
	binary   string
	env      map[string]string
	files    map[string]string
	keepTemp bool
}

var (
	setupNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	setupEnvNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	setupVariablePattern = regexp.MustCompile(`\$\{(?:tmp|binary|subject\.binary|file\.[A-Za-z0-9._-]+|setup\.dir|setup\.files\.[A-Za-z0-9._-]+)\}`)
)

func validateSetup(setup Setup) []validationError {
	var errs []validationError
	for envFileIndex, envFile := range setup.EnvFiles {
		path := fmt.Sprintf("setup.envFiles[%d]", envFileIndex)
		if err := validateSetupEnvFilePath(envFile); err != nil {
			errs = append(errs, validationError{Path: path, Message: err.Error()})
		}
	}

	fileNames := map[string]int{}
	for fileIndex, file := range setup.Files {
		path := fmt.Sprintf("setup.files[%d]", fileIndex)
		if strings.TrimSpace(file.Name) == "" {
			errs = append(errs, validationError{Path: path + ".name", Message: "is required"})
		} else if !setupNamePattern.MatchString(file.Name) {
			errs = append(errs, validationError{Path: path + ".name", Message: "must use letters, numbers, dot, dash, or underscore"})
		}
		if file.Name != "" {
			if previous, ok := fileNames[file.Name]; ok {
				errs = append(errs, validationError{Path: path + ".name", Message: fmt.Sprintf("must be unique, already used by setup.files[%d]", previous)})
			}
			fileNames[file.Name] = fileIndex
		}
		if err := validateSetupFilePath(file.Path); err != nil {
			errs = append(errs, validationError{Path: path + ".path", Message: err.Error()})
		}
	}

	for envIndex, name := range setup.RequireEnv {
		path := fmt.Sprintf("setup.requireEnv[%d]", envIndex)
		if strings.TrimSpace(name) == "" {
			errs = append(errs, validationError{Path: path, Message: "is required"})
		} else if !setupEnvNamePattern.MatchString(name) {
			errs = append(errs, validationError{Path: path, Message: "must use environment variable name syntax"})
		}
	}

	for buildIndex, command := range setup.Build {
		if strings.TrimSpace(command) == "" {
			errs = append(errs, validationError{Path: fmt.Sprintf("setup.build[%d]", buildIndex), Message: "must not be empty"})
		}
	}

	serviceNames := map[string]int{}
	for serviceIndex, service := range setup.Services {
		path := fmt.Sprintf("setup.services[%d]", serviceIndex)
		if strings.TrimSpace(service.Name) == "" {
			errs = append(errs, validationError{Path: path + ".name", Message: "is required"})
		} else if !setupNamePattern.MatchString(service.Name) {
			errs = append(errs, validationError{Path: path + ".name", Message: "must use letters, numbers, dot, dash, or underscore"})
		}
		if service.Name != "" {
			if previous, ok := serviceNames[service.Name]; ok {
				errs = append(errs, validationError{Path: path + ".name", Message: fmt.Sprintf("must be unique, already used by setup.services[%d]", previous)})
			}
			serviceNames[service.Name] = serviceIndex
		}
		if len(service.Args) == 0 && strings.TrimSpace(service.Shell) == "" {
			errs = append(errs, validationError{Path: path + ".args", Message: "must set args or shell"})
		}
		if service.StopTimeout != "" {
			if _, err := time.ParseDuration(service.StopTimeout); err != nil {
				errs = append(errs, validationError{Path: path + ".stopTimeout", Message: "must be a duration"})
			}
		}
		if service.Ready.Timeout != "" {
			if _, err := time.ParseDuration(service.Ready.Timeout); err != nil {
				errs = append(errs, validationError{Path: path + ".ready.timeout", Message: "must be a duration"})
			}
		}
	}
	return errs
}

func validateSetupEnvFilePath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("is required")
	}
	if strings.ContainsRune(trimmed, 0) {
		return fmt.Errorf("must not contain NUL")
	}
	if strings.HasPrefix(trimmed, "~") && trimmed != "~" && !strings.HasPrefix(trimmed, "~/") {
		return fmt.Errorf("only ~/ home expansion is supported")
	}
	return nil
}

func validateSetupFilePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("is required")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("must be relative")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must stay inside the setup temp directory")
	}
	return nil
}

func prepareSetup(spec *Spec, keepTemp bool) (*setupContext, []validationError) {
	if errs := validateSetup(spec.Setup); len(errs) > 0 {
		return nil, errs
	}

	ctx := &setupContext{
		env:      map[string]string{},
		files:    map[string]string{},
		keepTemp: keepTemp,
	}
	loadedEnv, envErrs := loadSetupEnvFiles(spec.Setup.EnvFiles)
	if len(envErrs) > 0 {
		return nil, envErrs
	}
	ctx.env = loadedEnv

	if errs := ctx.checkRequiredEnv(spec.Setup.RequireEnv); len(errs) > 0 {
		return nil, errs
	}

	if len(spec.Setup.Files) == 0 && len(spec.Setup.Build) == 0 && len(spec.Setup.Services) == 0 {
		return ctx, nil
	}

	dir, err := os.MkdirTemp("", "signet-")
	if err != nil {
		return nil, []validationError{{Path: "setup.files", Message: err.Error()}}
	}
	ctx.dir = dir

	for fileIndex, file := range spec.Setup.Files {
		target := filepath.Join(dir, filepath.Clean(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			ctx.cleanup()
			return nil, []validationError{{Path: fmt.Sprintf("setup.files[%d].path", fileIndex), Message: err.Error()}}
		}
		content, err := ctx.expandString(file.Content)
		if err != nil {
			ctx.cleanup()
			return nil, []validationError{{Path: fmt.Sprintf("setup.files[%d].content", fileIndex), Message: err.Error()}}
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			ctx.cleanup()
			return nil, []validationError{{Path: fmt.Sprintf("setup.files[%d].path", fileIndex), Message: err.Error()}}
		}
		ctx.files[file.Name] = target
	}
	return ctx, nil
}

func loadSetupEnvFiles(paths []string) (map[string]string, []validationError) {
	env := map[string]string{}
	for index, path := range paths {
		resolved, err := expandSetupEnvFilePath(path)
		if err != nil {
			return nil, []validationError{{Path: fmt.Sprintf("setup.envFiles[%d]", index), Message: err.Error()}}
		}
		values, err := loadDotenvFile(resolved)
		if err != nil {
			return nil, []validationError{{Path: fmt.Sprintf("setup.envFiles[%d]", index), Message: err.Error()}}
		}
		for name, value := range values {
			if _, exists := os.LookupEnv(name); exists {
				continue
			}
			env[name] = value
		}
	}
	return env, nil
}

func expandSetupEnvFilePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, trimmed[2:]), nil
	}
	if strings.HasPrefix(trimmed, "~") {
		return "", fmt.Errorf("only ~/ home expansion is supported")
	}
	return trimmed, nil
}

func loadDotenvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	env := map[string]string{}
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		name, value, ok, err := parseDotenvLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if !ok {
			continue
		}
		env[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return env, nil
}

func parseDotenvLine(line string) (string, string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}

	equals := strings.Index(trimmed, "=")
	if equals < 0 {
		return "", "", false, fmt.Errorf("must use KEY=VALUE syntax")
	}
	name := strings.TrimSpace(trimmed[:equals])
	if !setupEnvNamePattern.MatchString(name) {
		return "", "", false, fmt.Errorf("invalid environment variable name %q", name)
	}
	value, err := parseDotenvValue(strings.TrimSpace(trimmed[equals+1:]))
	if err != nil {
		return "", "", false, err
	}
	return name, value, true, nil
}

func parseDotenvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '\'':
		return parseSingleQuotedDotenvValue(value)
	case '"':
		return parseDoubleQuotedDotenvValue(value)
	default:
		return stripDotenvComment(value), nil
	}
}

func parseSingleQuotedDotenvValue(value string) (string, error) {
	end := strings.Index(value[1:], "'")
	if end < 0 {
		return "", fmt.Errorf("unterminated quoted value")
	}
	end++
	rest := strings.TrimSpace(value[end+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("unexpected content after quoted value")
	}
	return value[1:end], nil
}

func parseDoubleQuotedDotenvValue(value string) (string, error) {
	var parsed strings.Builder
	escaped := false
	for index := 1; index < len(value); index++ {
		next := value[index]
		if escaped {
			switch next {
			case 'n':
				parsed.WriteByte('\n')
			case 'r':
				parsed.WriteByte('\r')
			case 't':
				parsed.WriteByte('\t')
			default:
				parsed.WriteByte(next)
			}
			escaped = false
			continue
		}
		switch next {
		case '\\':
			escaped = true
		case '"':
			rest := strings.TrimSpace(value[index+1:])
			if rest != "" && !strings.HasPrefix(rest, "#") {
				return "", fmt.Errorf("unexpected content after quoted value")
			}
			return parsed.String(), nil
		default:
			parsed.WriteByte(next)
		}
	}
	return "", fmt.Errorf("unterminated quoted value")
}

func stripDotenvComment(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimSpace(value[:index])
		}
	}
	return strings.TrimSpace(value)
}

func (ctx *setupContext) checkRequiredEnv(names []string) []validationError {
	var errs []validationError
	for index, name := range names {
		value, ok := ctx.lookupEnv(name)
		if !ok || value == "" {
			errs = append(errs, validationError{
				Path:    fmt.Sprintf("setup.requireEnv[%d]", index),
				Message: fmt.Sprintf("environment variable %s is required", name),
			})
		}
	}
	return errs
}

func (ctx *setupContext) lookupEnv(name string) (string, bool) {
	if value, ok := os.LookupEnv(name); ok {
		return value, true
	}
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.env[name]
	return value, ok
}

func (ctx *setupContext) commandEnv() []string {
	if ctx == nil || len(ctx.env) == 0 {
		return nil
	}
	env := os.Environ()
	keys := make([]string, 0, len(ctx.env))
	for name := range ctx.env {
		if _, exists := os.LookupEnv(name); exists {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		env = append(env, name+"="+ctx.env[name])
	}
	return env
}

func (ctx *setupContext) cleanup() {
	if ctx == nil || ctx.keepTemp || ctx.dir == "" {
		return
	}
	_ = os.RemoveAll(ctx.dir)
}

func (ctx *setupContext) expandString(value string) (string, error) {
	if value == "" {
		return value, nil
	}
	var expandErr error
	expanded := setupVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		if expandErr != nil {
			return token
		}
		name := strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}")
		replacement, ok := ctx.lookup(name)
		if !ok {
			expandErr = fmt.Errorf("unknown setup variable %s", token)
			return token
		}
		return replacement
	})
	if expandErr != nil {
		return "", expandErr
	}
	return expanded, nil
}

func (ctx *setupContext) expandStrings(values []string) ([]string, error) {
	expanded := make([]string, 0, len(values))
	for _, value := range values {
		next, err := ctx.expandString(value)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, next)
	}
	return expanded, nil
}

func (ctx *setupContext) lookup(name string) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if (name == "binary" || name == "subject.binary") && ctx.binary != "" {
		return ctx.binary, true
	}
	if (name == "tmp" || name == "setup.dir") && ctx.dir != "" {
		return ctx.dir, true
	}
	for _, filePrefix := range []string{"file.", "setup.files."} {
		if strings.HasPrefix(name, filePrefix) {
			path, ok := ctx.files[strings.TrimPrefix(name, filePrefix)]
			return path, ok
		}
	}
	return "", false
}

func expandStep(step Step, setup *setupContext) (Step, error) {
	var err error
	step.Run.Args, err = setup.expandStrings(step.Run.Args)
	if err != nil {
		return step, err
	}
	step.Run.Shell, err = setup.expandString(step.Run.Shell)
	if err != nil {
		return step, err
	}
	step.Run.Stdin, err = setup.expandString(step.Run.Stdin)
	if err != nil {
		return step, err
	}
	return step, nil
}
