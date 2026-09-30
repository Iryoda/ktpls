package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Reading the model runs Gradle, which takes seconds; the model only
// changes with the build files, so it is cached against a fingerprint of
// them.

// IsBuildFile reports whether path is a Gradle build file: a change to it
// may change the project model.
func IsBuildFile(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".gradle") || strings.HasSuffix(base, ".gradle.kts") ||
		base == "gradle.properties" || base == "libs.versions.toml" || base == "gradle-wrapper.properties"
}

// skipDirs are directories never holding build files of the project.
var skipDirs = map[string]bool{".git": true, ".gradle": true, ".idea": true, "build": true, "node_modules": true, "out": true}

// Fingerprint identifies the state of a project's build: its build files
// (paths, sizes and times) and the environment Gradle runs with.
func Fingerprint(root string, env []string) (string, error) {
	h := sha256.New()
	h.Write(modelScript) // a new ktpls may read more of the model
	for _, e := range slices.Sorted(slices.Values(env)) {
		fmt.Fprintln(h, "env", e)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: ignore
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsBuildFile(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintln(h, path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// Model returns the path of the project model, from the cache if the
// build is unchanged since it was written (and its jars still exist),
// else by running Gradle (WriteModel). cached reports which.
func Model(ctx context.Context, root string, gradle, env []string) (path string, cached bool, err error) {
	dir, err := cacheDir(root)
	if err != nil {
		return "", false, err
	}
	fp, err := Fingerprint(root, env)
	if err != nil {
		return "", false, err
	}
	path = filepath.Join(dir, "model.txt")
	stamp := filepath.Join(dir, "model.fingerprint")
	if old, err := os.ReadFile(stamp); err == nil && string(old) == fp && jarsExist(path) {
		return path, true, nil
	}
	if path, err = WriteModel(ctx, root, gradle, env); err != nil {
		return "", false, err
	}
	return path, false, os.WriteFile(stamp, []byte(fp), 0o644)
}

// jarsExist reports whether the model at path exists and every jar on its
// classpaths does too (a cleaned Gradle cache invalidates the model).
func jarsExist(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 4 || fields[0] != "KTPLS-CLASSPATH" {
			continue
		}
		for _, p := range filepath.SplitList(fields[3]) {
			if !strings.HasSuffix(p, ".jar") {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				return false
			}
		}
	}
	return sc.Err() == nil
}

// RefreshModel rewrites the model by running Gradle, and reports whether
// it changed: a check on a model used from the cache.
func RefreshModel(ctx context.Context, root string, gradle, env []string) (changed bool, err error) {
	dir, err := cacheDir(root)
	if err != nil {
		return false, err
	}
	fp, err := Fingerprint(root, env)
	if err != nil {
		return false, err
	}
	old, _ := os.ReadFile(filepath.Join(dir, "model.txt"))
	path, err := WriteModel(ctx, root, gradle, env)
	if err != nil {
		return false, err
	}
	now, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(old, now), os.WriteFile(filepath.Join(dir, "model.fingerprint"), []byte(fp), 0o644)
}
