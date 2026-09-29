package protocol

import "testing"

func TestURIRoundTrip(t *testing.T) {
	for _, path := range []string{
		"/home/user/project/Main.kt",
		"/home/user/my project/Main.kt",
		"/tmp/ünïcode/Ä.kt",
	} {
		uri := URIFromPath(path)
		got, err := uri.Path()
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		if got != path {
			t.Errorf("round trip %q -> %q -> %q", path, uri, got)
		}
	}
}

func TestURIPath(t *testing.T) {
	// Neovim percent-encodes spaces; other clients may encode more.
	got, err := DocumentURI("file:///home/user/my%20project/A.kt").Path()
	if err != nil || got != "/home/user/my project/A.kt" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := DocumentURI("jdt://contents/foo").Path(); err == nil {
		t.Error("non-file scheme: expected error")
	}
}
