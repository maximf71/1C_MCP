package installer

import (
	"context"
	"embed"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ExtensionName = extensionName

type Options struct {
	Platform        string
	PlatformVersion string
	Infobase        string
	Server          bool
	User            string
	Password        string
}

func Install(ctx context.Context, source embed.FS, platform, infobase, user, password string) error {
	return InstallWithOptions(ctx, source, Options{Platform: platform, Infobase: infobase, User: user, Password: password})
}

func InstallWithOptions(ctx context.Context, source embed.FS, options Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(options.Infobase) == "" {
		return errors.New("infobase is required")
	}
	return installUpstream(source, options.Infobase, options.Server, options.Platform, options.User, options.Password, options.PlatformVersion)
}

// DiscoverPlatforms returns all exact executable paths newest-first. Setup
// persists the selected path, so serving a profile never silently switches the
// platform version.
func DiscoverPlatforms() []string {
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "1cv8"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "1cv8"),
	}
	seen := map[string]bool{}
	var result []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "bin", "1cv8.exe")
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				key := strings.ToLower(filepath.Clean(path))
				if !seen[key] {
					seen[key] = true
					result = append(result, path)
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return comparePlatformPath(result[i], result[j]) > 0 })
	return result
}

func PlatformVersion(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Clean(path))))
}

func comparePlatformPath(left, right string) int {
	leftParts := versionParts(PlatformVersion(left))
	rightParts := versionParts(PlatformVersion(right))
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		var a, b int
		if index < len(leftParts) {
			a = leftParts[index]
		}
		if index < len(rightParts) {
			b = rightParts[index]
		}
		if a > b {
			return 1
		}
		if a < b {
			return -1
		}
	}
	return 0
}

func versionParts(value string) []int {
	var result []int
	for _, part := range strings.Split(value, ".") {
		var number int
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			number = number*10 + int(r-'0')
		}
		result = append(result, number)
	}
	return result
}
