package server

import (
	"context"

	"github.com/Iryoda/ktpls/internal/cache"
	"github.com/Iryoda/ktpls/internal/kotlin"
	"github.com/Iryoda/ktpls/internal/protocol"
)

func (s *Server) PrepareRename(ctx context.Context, params *protocol.PrepareRenameParams) (*protocol.PrepareRenameResult, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var res *protocol.PrepareRenameResult
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		var rng protocol.Range
		if rng, err = kotlin.PrepareRename(f, sn.Index(), f.Mapper.PositionOffset(params.Position)); err != nil {
			err = protocol.Errorf(protocol.CodeRequestFailed, "%v", err)
			return
		}
		start, end := f.Mapper.RangeOffsets(rng)
		res = &protocol.PrepareRenameResult{Range: rng, Placeholder: string(f.Content[start:end])}
	})
	return res, err
}

func (s *Server) Rename(ctx context.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	path, err := params.TextDocument.URI.Path()
	if err != nil {
		return nil, err
	}
	var edit *protocol.WorkspaceEdit
	s.session.Read(func(sn *cache.Snapshot) {
		var f *kotlin.ParsedFile
		var release func()
		if f, release, err = sn.Parse(path); err != nil {
			return
		}
		defer release()
		var changes map[protocol.DocumentURI][]protocol.TextEdit
		changes, err = kotlin.Rename(f, sn.Index(), f.Mapper.PositionOffset(params.Position), params.NewName, sn.FilesContaining)
		if err != nil {
			err = protocol.Errorf(protocol.CodeRequestFailed, "%v", err)
			return
		}
		edit = &protocol.WorkspaceEdit{Changes: changes}
	})
	return edit, err
}
