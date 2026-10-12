package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/greatliontech/gofresh/gotool"
)

// goFlag keeps the argument and its original quoted spelling together. Replacing
// one flag must not re-tokenize a different flag's space-bearing value.
type goFlag struct{ argument, spelling string }

// quotedFields follows cmd/internal/quoted: only a leading quote encloses a
// field, no escaping occurs inside it, and interior quotes remain literal.
func quotedFields(text string) ([]goFlag, error) {
	var flags []goFlag
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		if text == "" {
			return flags, nil
		}
		var argument, spelling string
		if quote := text[0]; quote == '\'' || quote == '"' {
			end := strings.IndexByte(text[1:], quote)
			if end < 0 {
				return nil, fmt.Errorf("gomutant: GOFLAGS has an unterminated quoted argument")
			}
			argument, spelling = text[1:1+end], text[:2+end]
			text = text[2+end:]
		} else {
			end := strings.IndexAny(text, " \t\r\n")
			if end < 0 {
				end = len(text)
			}
			argument, spelling, text = text[:end], text[:end], text[end:]
		}
		flags = append(flags, goFlag{argument, spelling})
	}
}

func parseGoFlags(text string) ([]goFlag, error) {
	flags, err := quotedFields(text)
	if err != nil {
		return nil, err
	}
	for _, flag := range flags {
		argument := flag.argument
		if !strings.HasPrefix(argument, "-") || argument == "-" || argument == "--" ||
			strings.HasPrefix(argument, "---") || strings.HasPrefix(argument, "-=") || strings.HasPrefix(argument, "--=") {
			return nil, fmt.Errorf("gomutant: GOFLAGS contains non-flag %q", argument)
		}
	}
	return flags, nil
}

// parseBuildTags is cmd/go's tag-value grammar: its legacy quoted/space form
// takes precedence over comma splitting. Empty comma elements are ignored.
func parseBuildTags(value string) ([]string, error) {
	var tags []string
	if strings.ContainsAny(value, " '") {
		fields, err := quotedFields(value)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			tags = append(tags, field.argument)
		}
		return tags, nil
	}
	for _, tag := range strings.Split(value, ",") {
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func (f goFlag) parts() (name, value string, assigned bool) {
	name, value, assigned = strings.Cut(strings.TrimPrefix(strings.TrimPrefix(f.argument, "-"), "-"), "=")
	return name, value, assigned
}

// oracleFlags admits only a selection that cannot subtract tests from the
// explicitly requested oracle. Both spellings name the same Go flag; repeats
// have the same last-setting-wins meaning as the go command.
func oracleFlags(text string) error {
	flags, err := parseGoFlags(text)
	if err != nil {
		return err
	}
	skip := ""
	for _, flag := range flags {
		name, value, assigned := flag.parts()
		if strings.TrimPrefix(name, "test.") != "skip" {
			continue
		}
		if !assigned {
			return fmt.Errorf("gomutant: GOFLAGS skip requires a value")
		}
		skip = value
	}
	if skip != "" {
		return fmt.Errorf("gomutant: GOFLAGS skip %q can omit required oracle tests; remove the skip setting and select the oracle explicitly", skip)
	}
	return nil
}

// resolvedOracleFlags checks the go command's effective value, including a
// persistent GOENV setting when the process carries no nonempty override.
func resolvedOracleFlags(ctx context.Context, dir string, env []string) error {
	value, _ := gotool.LookupEnv(env, "GOFLAGS")
	if value == "" {
		snapshot, err := goRunner.TakeEnvSnapshot(ctx, dir, env)
		if err != nil {
			return err
		}
		value = snapshot.Value("GOFLAGS")
	}
	return oracleFlags(value)
}
