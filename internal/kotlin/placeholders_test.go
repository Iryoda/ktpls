package kotlin

import (
	"strings"
	"testing"
)

func TestPlaceholders(t *testing.T) {
	const src = `package acme

@FeignClient(
    name = "billing",
    url = "\${services.billing.host}:" +
        "\${services.billing.port:8080}",
)
interface BillingClient

class Jobs(@Value("\${jobs.cron:\${jobs.default-cron}}") val cron: String) {
    val name = "$cron \\${not.a.placeholder} \${open.only"
    val sql = """select ${'$'}{db.schema}.t"""
    val raw = """${'$'}{x} ${cron}"""
}
`
	f, _ := parseOne(t, "/w/acme/Jobs.kt", src)
	var got []string
	for _, p := range Placeholders(f) {
		s := p.Key
		if p.HasDefault {
			s += ":" + p.Default
		}
		// The range covers the key.
		start := f.Mapper.PositionOffset(p.Range.Start)
		end := f.Mapper.PositionOffset(p.Range.End)
		if string(f.Content[start:end]) != p.Key {
			t.Errorf("range of %s covers %q", p.Key, f.Content[start:end])
		}
		got = append(got, s)
	}
	want := "services.billing.host services.billing.port:8080 jobs.cron:\\${jobs.default-cron} jobs.default-cron db.schema x"
	if strings.Join(got, " ") != want {
		t.Errorf("placeholders =\n  %s\nwant\n  %s", strings.Join(got, " "), want)
	}

	off := strings.Index(src, "billing.port") + 3
	if p := PlaceholderAt(f, off); p == nil || p.Key != "services.billing.port" {
		t.Errorf("PlaceholderAt = %+v", p)
	}
	if p := PlaceholderAt(f, strings.Index(src, `"billing"`)+2); p != nil {
		t.Errorf("PlaceholderAt in a plain string = %+v", p)
	}
}
