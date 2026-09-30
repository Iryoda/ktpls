package messages

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	src := "# comment\n! also\n" +
		"a.b=one\n" +
		"  c.d : two\n" +
		"e.f three\n" +
		"g.h=multi \\\n    line\n" +
		"i\\=j=escaped key\n" +
		"k=caf\\u00e9\n" +
		"empty=\n" +
		"bare\n"
	var got []string
	for _, p := range Parse([]byte(src)) {
		got = append(got, p.Key+"|"+p.Value)
	}
	want := []string{"a.b|one", "c.d|two", "e.f|three", "g.h|multi line", "i=j|escaped key", "k|café", "empty|", "bare|"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if p := Parse([]byte(src)); p[3].Line != 5 || p[4].Line != 7 {
		t.Errorf("lines: %d, %d", p[3].Line, p[4].Line)
	}
}

func TestLocaleOf(t *testing.T) {
	for name, want := range map[string]string{
		"messages.properties": "", "messages_pt_BR.properties": "pt_BR", "messages_en.properties": "en",
	} {
		if got, ok := localeOf(name, "messages"); !ok || got != want {
			t.Errorf("localeOf(%q) = %q, %v", name, got, ok)
		}
	}
	for _, name := range []string{"messages-extra.properties", "messagesX.properties", "other.properties"} {
		if _, ok := localeOf(name, "messages"); ok {
			t.Errorf("localeOf(%q): a bundle file", name)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	res := filepath.Join(root, "app/src/main/resources")
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(res, "application.yml"), "server:\n  port: 8080\nspring:\n  messages:\n    basename: i18n/errors, labels # two bundles\n")
	write(filepath.Join(res, "i18n/errors.properties"), "user.not-found=User not found\n")
	write(filepath.Join(res, "i18n/errors_pt_BR.properties"), "user.not-found=Usuário não encontrado\n")
	write(filepath.Join(res, "labels.properties"), "label.ok=OK\n")
	write(filepath.Join(res, "messages.properties"), "not.used=x\n") // not a configured basename
	write(filepath.Join(root, "app/src/test/resources/i18n/errors.properties"), "test.only=x\n")

	b := Load(root)
	if !b.Has("user.not-found") || !b.Has("label.ok") || b.Has("not.used") || b.Has("test.only") {
		t.Errorf("keys: %v", b.Keys)
	}
	if es := b.Keys["user.not-found"]; len(es) != 2 || es[0].Locale != "" || es[1].Locale != "pt_BR" || es[1].Value != "Usuário não encontrado" {
		t.Errorf("entries: %+v", es)
	}
	if b.Stale() {
		t.Errorf("stale right after loading")
	}
	write(filepath.Join(res, "labels.properties"), "label.ok=OK\nlabel.cancel=Cancel\n")
	os.Chtimes(filepath.Join(res, "labels.properties"), timeLater(), timeLater())
	if !b.Stale() {
		t.Errorf("not stale after an edit")
	}
}

func timeLater() time.Time { return time.Now().Add(time.Minute) }
