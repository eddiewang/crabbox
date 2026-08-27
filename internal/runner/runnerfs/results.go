package runnerfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	AutoMaxFiles      = 50
	AutoMaxFileBytes  = 16 << 20
	AutoMaxTotalBytes = 64 << 20
	AutoSniffBytes    = 4 << 10
	AutoFailureBytes  = 1 << 20
)

type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ResultOptions struct {
	Paths              []string
	Auto               bool
	After              time.Time
	ExplicitMaxBytes   int64
	ExplicitTotalBytes int64
}

type Results struct {
	Files    []File
	Warnings []Warning
}

type resultCandidate struct {
	name   string
	failed bool
}

var junitFailure = regexp.MustCompile(`<(failure|error)([\t\r\n >])`)

// CollectResults reads explicit files before bounded automatic discovery. An
// external symlink is never treated as a report, even if its filename matches.
func (r *Root) CollectResults(ctx context.Context, options ResultOptions) (Results, error) {
	var result Results
	var identities []os.FileInfo
	total := int64(0)
	if options.ExplicitMaxBytes < 1 || options.ExplicitTotalBytes < 1 {
		if len(options.Paths) != 0 {
			return result, errors.New("explicit result byte limits must be positive")
		}
	}
	for _, name := range options.Paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		remaining := options.ExplicitTotalBytes - total
		limit := min(options.ExplicitMaxBytes, remaining)
		file, err := r.readDistinct(name, limit, identities)
		if err != nil {
			if errors.Is(err, ErrLimit) || errors.Is(err, ErrChanged) {
				result.Warnings = append(result.Warnings, Warning{name, err.Error()})
			}
			continue
		}
		result.Files = append(result.Files, file)
		identities = append(identities, file.identity)
		total += int64(len(file.Data))
	}
	if !options.Auto {
		return result, nil
	}
	candidates, err := r.resultCandidates(ctx, options.After)
	if err != nil {
		return result, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].failed != candidates[j].failed {
			return candidates[i].failed
		}
		return candidates[i].name < candidates[j].name
	})
	autoTotal := int64(0)
	for index, candidate := range candidates {
		if index >= AutoMaxFiles {
			break
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		file, err := r.readDistinct(candidate.name, AutoMaxFileBytes, identities)
		if err != nil {
			if errors.Is(err, ErrLimit) {
				result.Warnings = append(result.Warnings, Warning{candidate.name, fmt.Sprintf("report exceeds %d-byte per-file limit", AutoMaxFileBytes)})
			} else if errors.Is(err, ErrChanged) {
				result.Warnings = append(result.Warnings, Warning{candidate.name, err.Error()})
			}
			continue
		}
		if !options.After.IsZero() && file.ModTime.Before(options.After) {
			continue
		}
		if !bytes.Contains(file.Data[:min(len(file.Data), AutoSniffBytes)], []byte("<testsuite")) {
			continue
		}
		if autoTotal+int64(len(file.Data)) > AutoMaxTotalBytes {
			result.Warnings = append(result.Warnings, Warning{candidate.name, fmt.Sprintf("report exceeds remaining %d-byte aggregate limit", AutoMaxTotalBytes)})
			continue
		}
		autoTotal += int64(len(file.Data))
		result.Files = append(result.Files, file)
		identities = append(identities, file.identity)
	}
	return result, nil
}

func (r *Root) resultCandidates(ctx context.Context, after time.Time) ([]resultCandidate, error) {
	var passing, failing []resultCandidate
	err := r.walkDirectory(ctx, ".", func(name string, entry fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			directory := entry.Name()
			if runtime.GOOS == "windows" {
				directory = strings.ToLower(directory)
			}
			if directory == ".git" || directory == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !junitFilename(entry.Name()) {
			return nil
		}
		file, err := r.openRegular(filepath.FromSlash(name))
		if err != nil {
			return nil
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || (!after.IsZero() && info.ModTime().Before(after)) {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(file, AutoFailureBytes))
		if err != nil || !bytes.Contains(data[:min(len(data), AutoSniffBytes)], []byte("<testsuite")) {
			return nil
		}
		candidate := resultCandidate{name, junitFailure.Match(data)}
		if candidate.failed {
			failing = retainResultCandidate(failing, candidate)
		} else {
			passing = retainResultCandidate(passing, candidate)
		}
		return nil
	})
	return append(failing, passing...), err
}

// Retain only the earliest candidates in each priority class while still
// inspecting the whole tree. Later failures must outrank earlier passing files.
func retainResultCandidate(values []resultCandidate, next resultCandidate) []resultCandidate {
	index := sort.Search(len(values), func(i int) bool { return values[i].name >= next.name })
	if index >= AutoMaxFiles {
		return values
	}
	values = append(values, resultCandidate{})
	copy(values[index+1:], values[index:])
	values[index] = next
	return values[:min(len(values), AutoMaxFiles)]
}

func junitFilename(name string) bool {
	return junitFilenameForOS(runtime.GOOS, name)
}

func junitFilenameForOS(goos, name string) bool {
	if goos == "windows" {
		lower := strings.ToLower(name)
		return lower == "results.xml" || strings.HasPrefix(lower, "junit") && strings.HasSuffix(lower, ".xml") || strings.HasPrefix(lower, "test-") && strings.HasSuffix(lower, ".xml")
	}
	return name == "results.xml" || strings.HasPrefix(name, "junit") && strings.HasSuffix(name, ".xml") || strings.HasPrefix(name, "TEST-") && strings.HasSuffix(name, ".xml")
}
