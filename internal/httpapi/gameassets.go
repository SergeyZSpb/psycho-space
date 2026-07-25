package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GameAssetPath prefixes the art-image URLs a game advertises in its config. It
// is game-agnostic on purpose: the blob store is shared infrastructure and the
// game key travels in the path, so a second game needs no new route and no new
// handler.
//
// URLs minted now must keep working after the legacy /api/game/assets/ alias is
// deleted, so config responses are always built from this constant.
const GameAssetPath = "/api/game-assets/"

// handleGameAsset serves one art image from the shared blob store. Public (art is
// not sensitive) and cacheable; the client downloads on demand.
func (s *Server) handleGameAsset(w http.ResponseWriter, r *http.Request) {
	if s.d.GameAssets == nil {
		writeError(w, r, http.StatusNotFound, "asset_not_found")
		return
	}
	b, ct, err := s.d.GameAssets.Bytes(r.Context(), chi.URLParam(r, "game"), chi.URLParam(r, "key"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "asset_not_found")
		return
	}
	w.Header().Set("Content-Type", imageContentType(ct))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: the bytes are an opaque image blob, not a rendered
	// document — the response is pinned to an allowlisted image content type
	// (imageContentType) and sent with X-Content-Type-Options: nosniff.
	_, _ = w.Write(b)
}

// imageContentType constrains a stored asset's content type to image types.
// The value is a plain column in game_assets, so anything able to write that
// table could otherwise have the app serve active content (text/html) from its
// own origin. Anything unrecognised is served as an opaque download instead.
func imageContentType(ct string) string {
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "image/webp":
		return "image/webp"
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	case "image/avif":
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}
