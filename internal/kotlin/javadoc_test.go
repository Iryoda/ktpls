package kotlin

import (
	"strings"
	"testing"
)

func TestRenderJavadoc(t *testing.T) {
	comment := `/**
	 * Saves a given entity. Use the returned instance for further operations as the save operation might have changed the
	 * entity instance completely.
	 * <p>
	 * Returns a {@link java.util.List List} of {@code Map<K, V>} values, see {@link #findAll()}.
	 * <ul>
	 *   <li>one</li>
	 *   <li><b>two</b> &amp; <i>three</i></li>
	 * </ul>
	 * <pre class="code">
	 * repository.save(entity);
	 * </pre>
	 *
	 * @param entity must not be {@literal null}.
	 * @return the saved entity; will never be {@literal null}.
	 * @throws IllegalArgumentException in case the given {@literal entity} is {@literal null}.
	 */`
	got := RenderJavadoc(comment)
	for _, want := range []string{
		"Saves a given entity. Use the returned instance",
		"completely.\n\nReturns a List of `Map<K, V>` values, see `findAll()`.",
		"- one",
		"- **two** & *three*",
		"```java\nrepository.save(entity);\n```",
		"`entity`",
		"must not be `null`.",
		"the saved entity; will never be `null`.",
		"IllegalArgumentException",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<") && !strings.Contains(got, "Map<K, V>") {
		t.Errorf("HTML left in:\n%s", got)
	}
}
