package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprint(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "build.gradle.kts"), "plugins {}")
	write(t, filepath.Join(root, "src/main/kotlin/A.kt"), "class A")
	write(t, filepath.Join(root, "build/tmp/x.gradle"), "ignored")
	fp := func() string {
		s, err := Fingerprint(root, []string{"JAVA_HOME=/jdk"})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := fp()

	write(t, filepath.Join(root, "src/main/kotlin/A.kt"), "class A { }")
	write(t, filepath.Join(root, "build/tmp/x.gradle"), "still ignored")
	if fp() != base {
		t.Errorf("a source file or build output changed the fingerprint")
	}

	write(t, filepath.Join(root, "gradle/libs.versions.toml"), "[versions]")
	if fp() == base {
		t.Errorf("a new version catalog kept the fingerprint")
	}
	base = fp()
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(root, "build.gradle.kts"), later, later)
	if fp() == base {
		t.Errorf("an edited build file kept the fingerprint")
	}
	if s, _ := Fingerprint(root, []string{"JAVA_HOME=/other"}); s == fp() {
		t.Errorf("another JAVA_HOME kept the fingerprint")
	}
}

func TestModelFromCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	write(t, filepath.Join(root, "build.gradle.kts"), "plugins {}")
	jar := filepath.Join(t.TempDir(), "lib.jar")
	write(t, jar, "")

	dir, err := cacheDir(root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "model.txt"), "KTPLS-SOURCES\t:\tmain\t/src\nKTPLS-CLASSPATH\t:\tmain\t"+jar+"\n")
	fp, _ := Fingerprint(root, nil)
	write(t, filepath.Join(dir, "model.fingerprint"), fp)

	// Gradle would fail: a hit must not run it.
	gradle := []string{"/nonexistent/gradlew"}
	if _, cached, err := Model(context.Background(), root, gradle, nil); err != nil || !cached {
		t.Fatalf("Model = cached %v, err %v; want a cache hit", cached, err)
	}
	os.Remove(jar)
	if _, _, err := Model(context.Background(), root, gradle, nil); err == nil {
		t.Errorf("a model with a missing jar was used from the cache")
	}
}
