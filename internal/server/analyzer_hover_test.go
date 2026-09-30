package server

import (
	"testing"

	"github.com/Iryoda/ktpls/internal/analyzer"
)

func TestHoverMarkdown(t *testing.T) {
	h := &analyzer.HoverInfo{
		Signature:   "fun <S : T> save(entity: S): S",
		Call:        "save(entity: Order): Order",
		Container:   "org.example.Repository",
		Doc:         "/**\n * Saves an entity; never {@literal null}.\n * @param entity the entity\n */",
		DocLanguage: "java",
	}
	want := "```kotlin\nfun <S : T> save(entity: S): S\n```\n\n" +
		"*in this call:*\n```kotlin\nsave(entity: Order): Order\n```\n\n" +
		"*in `org.example.Repository`*\n\n---\n\n" +
		"Saves an entity; never `null`.\n\n**Parameters**\n- `entity` — the entity"
	if got := hoverMarkdown(h); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
