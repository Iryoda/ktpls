package server

import (
	"slices"

	"github.com/Iryoda/ktpls/internal/cache"
)

// affectedFiles returns the files to re-diagnose after a rebuild: the
// saved ones, those showing diagnostics, and the open ones.
func (s *Server) affectedFiles(saved map[string]bool) []string {
	set := map[string]bool{}
	for p := range saved {
		set[p] = true
	}
	s.az.mu.Lock()
	for p := range s.az.diags {
		set[p] = true
	}
	s.az.mu.Unlock()
	s.session.Read(func(sn *cache.Snapshot) {
		for f := range sn.Files() {
			if f.Overlay {
				set[f.Path] = true
			}
		}
	})
	var out []string
	for p := range set {
		if cache.IsKotlinFile(p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	if len(out) > maxAffected {
		out = out[:maxAffected]
	}
	return out
}
