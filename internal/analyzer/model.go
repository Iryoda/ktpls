package analyzer

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// modelScript is a Gradle init script printing each project's source
// folders, compile classpath and Kotlin compiler plugin options.
//
//go:embed ktpls-model.gradle
var modelScript []byte

// WriteModel runs the project's Gradle with the model init script and
// writes the model to ktpls's cache directory, returning its path. gradle
// is the command running Gradle (e.g. ./gradlew) and env extra
// environment variables (e.g. JAVA_HOME).
func WriteModel(ctx context.Context, root string, gradle []string, env []string) (string, error) {
	dir, err := cacheDir(root)
	if err != nil {
		return "", err
	}
	script := filepath.Join(dir, "ktpls-model.gradle")
	if err := os.WriteFile(script, modelScript, 0o644); err != nil {
		return "", err
	}
	args := append(append([]string{}, gradle[1:]...), "-I", script, "ktplsModel", "-q", "--no-configuration-cache")
	cmd := exec.CommandContext(ctx, gradle[0], args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("reading the Gradle project model: %w: %s", err, lastLines(stderr.String(), 5))
	}
	var model bytes.Buffer
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "KTPLS-") {
			model.WriteString(line)
			model.WriteString("\n")
		}
	}
	if !bytes.Contains(model.Bytes(), []byte("KTPLS-SOURCES")) {
		return "", fmt.Errorf("the Gradle project has no JVM source sets")
	}
	path := filepath.Join(dir, "model.txt")
	return path, os.WriteFile(path, model.Bytes(), 0o644)
}

// CacheFile returns the path of a file named name in the project's cache
// directory.
func CacheFile(root, name string) (string, error) {
	dir, err := cacheDir(root)
	return filepath.Join(dir, name), err
}

// DownloadSources has Gradle download the sources jars of the project's
// libraries into its cache (those missing; a no-op once they are there),
// returning how many of how many libraries have sources.
func DownloadSources(ctx context.Context, root string, gradle, env []string) (have, all int, err error) {
	dir, err := cacheDir(root)
	if err != nil {
		return 0, 0, err
	}
	script := filepath.Join(dir, "ktpls-model.gradle")
	if err := os.WriteFile(script, modelScript, 0o644); err != nil {
		return 0, 0, err
	}
	args := append(append([]string{}, gradle[1:]...), "-I", script, "ktplsSources", "-q", "--no-configuration-cache")
	cmd := exec.CommandContext(ctx, gradle[0], args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("downloading sources: %w: %s", err, lastLines(stderr.String(), 5))
	}
	for line := range strings.SplitSeq(out.String(), "\n") {
		var project string
		var h, a int
		if _, err := fmt.Sscanf(line, "KTPLS-SOURCES-JARS\t%s\t%d\t%d", &project, &h, &a); err == nil {
			have, all = have+h, all+a
		}
	}
	return have, all, nil
}

// cacheDir returns (creating it) a per-project directory under the user
// cache directory.
func cacheDir(root string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(base, "ktpls", "projects", filepath.Base(root)+"-"+hex.EncodeToString(sum[:6]))
	return dir, os.MkdirAll(dir, 0o755)
}

// ClassArchive returns where to keep the JVM class data sharing archive
// of an analyzer jar (one per jar build), or "" if there is no cache
// directory.
func ClassArchive(jar string) string {
	info, err := os.Stat(jar)
	if err != nil {
		return ""
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(base, "ktpls")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	archive := filepath.Join(dir, fmt.Sprintf("analyzer-%d-%d.jsa", info.Size(), info.ModTime().Unix()))
	old, _ := filepath.Glob(filepath.Join(dir, "analyzer-*.jsa"))
	for _, p := range old {
		if p != archive {
			os.Remove(p) // an older jar's
		}
	}
	return archive
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// FindJar returns the analyzer jar: the configured path, else analyzer.jar
// next to the ktpls executable (following symlinks, as Mason links it).
func FindJar(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("analyzer jar: %w", err)
		}
		return configured, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	for _, p := range []string{
		filepath.Join(filepath.Dir(exe), "analyzer.jar"),
		filepath.Join(filepath.Dir(exe), "..", "share", "ktpls", "analyzer.jar"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("analyzer.jar not found next to %s", exe)
}

// FindJava returns the java executable of javaHome, else of $JAVA_HOME,
// else java on PATH.
func FindJava(javaHome string) (string, error) {
	for _, home := range []string{javaHome, os.Getenv("JAVA_HOME")} {
		if home == "" {
			continue
		}
		p := filepath.Join(home, "bin", "java")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("java")
}

// JavaHome returns the home directory of a java executable.
func JavaHome(java string) string {
	if real, err := filepath.EvalSymlinks(java); err == nil {
		java = real
	}
	return filepath.Dir(filepath.Dir(java))
}
