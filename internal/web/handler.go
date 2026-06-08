package web

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kapoost/humanmcp-go/internal/auth"
	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
)

type Handler struct {
	cfg               *config.Config
	store             *content.Store
	auth              *auth.Auth
	msgStore          *content.MessageStore
	statStore         *content.StatStore
	blobStore         *content.BlobStore
	listingStore      *content.ListingStore
	questionStore     *content.QuestionStore
	subscriptionStore *content.SubscriptionStore
	signingKey        *content.KeyPair
	tmpl              *template.Template
	startedAt         time.Time

	// IP-based sliding-window rate limiter for the anonymous /contact form.
	// Generous limit so a real human refining their message isn't blocked,
	// tight enough to stop bot-driven spam once we get visibility.
	contactRateMu      sync.Mutex
	contactRateLog     map[string][]time.Time
}

func NewHandler(cfg *config.Config, store *content.Store, a *auth.Auth) *Handler {
	h := &Handler{
		cfg:           cfg,
		store:         store,
		auth:          a,
		msgStore:      content.NewMessageStore(cfg.ContentDir),
		statStore:     content.NewStatStore(cfg.ContentDir),
		blobStore:     content.NewBlobStore(cfg.ContentDir),
		listingStore:      content.NewListingStore(cfg.ContentDir),
		questionStore:     content.NewQuestionStore(cfg.ContentDir),
		subscriptionStore: content.NewSubscriptionStore(cfg.ContentDir),
		startedAt:         time.Now(),
		contactRateLog:    make(map[string][]time.Time),
	}
	if cfg.SigningPrivateKey != "" {
		if kp, err := content.KeyPairFromBase64(cfg.SigningPrivateKey); err == nil {
			h.signingKey = kp
		}
	}
	funcs := template.FuncMap{
		"formatDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2 January 2006 15:04")
		},
		// signatureState returns one of "absent", "valid", "invalid". The
		// template branches on this — "active — authorship signed" is only
		// shown when the signature actually verifies against the owner's
		// public key. Previously the template only checked non-empty,
		// which let any garbage in the frontmatter masquerade as "signed".
		"signatureState": func(p *content.Piece) string {
			if p == nil || p.Signature == "" {
				return "absent"
			}
			if cfg.SigningPublicKey == "" {
				// No pubkey configured — we cannot verify, so we cannot
				// honestly claim "signed". Treat as invalid so the UI
				// is loud about the missing root of trust.
				return "invalid"
			}
			ok, _ := content.VerifyPiece(p, cfg.SigningPublicKey)
			if ok {
				return "valid"
			}
			return "invalid"
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("15:04")
		},
		"shortDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("02 Jan")
		},
		"isoDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02T15:04")
		},
		"lower": strings.ToLower,
		"filenameFromRef": func(ref string) string {
			parts := strings.SplitN(ref, "/", 2)
			if len(parts) == 2 {
				return parts[1]
			}
			return ref
		},
		"nl2br": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
		},
		"join":  func(slice []string, sep string) string { return strings.Join(slice, sep) },
		"slice": func(vals ...string) []string { return vals },
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
		"licenseLabel": func(s string) string {
			switch strings.ToLower(s) {
			case "cc-by", "ccby":
				return "CC BY"
			case "cc-by-sa", "ccbysa":
				return "CC BY-SA"
			case "cc-by-nc", "ccbync":
				return "CC BY-NC"
			case "cc-by-nd", "ccbynd":
				return "CC BY-ND"
			case "cc0":
				return "CC0"
			case "all-rights-reserved", "arr":
				return "All Rights Reserved"
			case "":
				return ""
			default:
				return s
			}
		},
		"otsHash": func(v interface{}) string {
			switch x := v.(type) {
			case string:
				return x
			case *content.Piece:
				if x == nil {
					return ""
				}
				return hex.EncodeToString(content.PiecePayload(x))
			case content.Piece:
				return hex.EncodeToString(content.PiecePayload(&x))
			}
			return fmt.Sprintf("%v", v)
		},
		"otsShort": func(s string) string {
			if len(s) < 12 {
				return s
			}
			return s[:8] + "…" + s[len(s)-4:]
		},
		"otsStatus": func(s string) string {
			return otsStatusOf(s)
		},
		"isHEIC": func(ref string) bool {
			lower := strings.ToLower(ref)
			return strings.HasSuffix(lower, ".heic") || strings.HasSuffix(lower, ".heif")
		},
		"not": func(v interface{}) bool {
			if v == nil {
				return true
			}
			switch b := v.(type) {
			case bool:
				return !b
			case string:
				return b == ""
			}
			return false
		},
	}
	h.tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(TemplatesFS, "templates/*"))
	return h
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/p/", h.handlePiece)
	mux.HandleFunc("/unlock/", h.handleUnlock)

	// Owner API (require edit token)
	mux.Handle("/api/content", h.auth.RequireOwner(http.HandlerFunc(h.handleAPIList)))
	mux.Handle("/api/content/", h.auth.RequireOwner(http.HandlerFunc(h.handleAPIContent)))

	// Well-known MCP discovery
	mux.HandleFunc("/.well-known/mcp-server.json", h.handleWellKnown)
	mux.HandleFunc("/.well-known/agent.json", h.handleAgentCard)

	// Dashboard (owner only)
	mux.Handle("/dashboard", h.auth.RequireOwner(http.HandlerFunc(h.handleDashboard)))

	// Messages (owner only)
	mux.Handle("/messages", h.auth.RequireOwner(http.HandlerFunc(h.handleMessages)))
	mux.Handle("/api/messages/", h.auth.RequireOwner(http.HandlerFunc(h.handleDeleteMessage)))

	// Contact form (public)
	mux.HandleFunc("/contact", h.handleContact)

	// Connect page (public)
	mux.HandleFunc("/connect", h.handleConnect)

	// Raw file serving (images etc)
	mux.HandleFunc("/files/", h.handleFile)

	// Image gallery (public)
	mux.HandleFunc("/images", h.handleImages)

	// SEO / crawl
	mux.HandleFunc("/robots.txt", h.handleRobots)
	mux.HandleFunc("/sitemap.xml", h.handleSitemap)
	mux.HandleFunc("/rss.xml", h.handleRSS)

	// New post page (owner only)
	mux.Handle("/new", h.auth.RequireOwner(http.HandlerFunc(h.handleNew)))

	// Edit page (owner only)
	mux.Handle("/edit/", h.auth.RequireOwner(http.HandlerFunc(h.handleEdit)))

	// Delete (owner only, POST)
	mux.Handle("/delete/", h.auth.RequireOwner(http.HandlerFunc(h.handleDelete)))
	mux.Handle("/timestamp/", h.auth.RequireOwner(http.HandlerFunc(h.handleTimestamp)))
	mux.Handle("/timestamp-all", h.auth.RequireOwner(http.HandlerFunc(h.handleTimestampAll)))


	// Skills API (owner only)
	mux.Handle("/api/skills", h.auth.RequireOwner(http.HandlerFunc(h.handleAPISkills)))
	mux.Handle("/api/skills/", h.auth.RequireOwner(http.HandlerFunc(h.handleAPISkills)))

	// Blob uploader UI (owner only)
	mux.Handle("/upload", h.auth.RequireOwner(http.HandlerFunc(h.handleUploadPage)))

	// Blob upload (owner only)
	mux.Handle("/api/blobs", h.auth.RequireOwner(http.HandlerFunc(h.handleAPIBlobs)))
	mux.Handle("/api/blobs/", h.auth.RequireOwner(http.HandlerFunc(h.handleAPIBlobs)))

	// Login/logout for web UI
	mux.HandleFunc("/login", h.handleLogin)
	mux.HandleFunc("/logout", h.handleLogout)

	// Recovered v273 routes
	mux.HandleFunc("/listings", h.handleListings)
	mux.HandleFunc("/listings/", h.handleListings)
	mux.HandleFunc("/artworks", h.handleArtworks)
	mux.HandleFunc("/artworks/", h.handleArtworks)
	mux.Handle("/mc", h.auth.RequireOwner(http.HandlerFunc(h.handleMissionControl)))
	mux.HandleFunc("/team", h.handleTeam)
	mux.HandleFunc("/personas", h.handlePersonasPage)
	mux.HandleFunc("/skills", h.handleSkillsPage)
	mux.Handle("/questions", h.auth.RequireOwner(http.HandlerFunc(h.handleQuestions)))
	// Both bare path (form-based POST from templates) and trailing-slash
	// (legacy / URL-based id) work — handler reads id from either.
	mux.Handle("/questions/answer", h.auth.RequireOwner(http.HandlerFunc(h.handleAnswerQuestion)))
	mux.Handle("/questions/answer/", h.auth.RequireOwner(http.HandlerFunc(h.handleAnswerQuestion)))
	mux.HandleFunc("/for-agents", h.handleForAgents)
	mux.HandleFunc("/subscribe", h.handleSubscribeForm)
	mux.HandleFunc("/subscribe/confirm", h.handleSubscribeConfirm)
	mux.HandleFunc("/llms.txt", h.handleLLMSTxt)
	mux.Handle("/llms-edit", h.auth.RequireOwner(http.HandlerFunc(h.handleLLMSTxtEdit)))
	mux.HandleFunc("/stats", h.handleStats)
	mux.HandleFunc("/gallery", h.handleGallery)
}

func (h *Handler) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"$schema":     "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
		"name":        "io.github.kapoost/humanmcp",
		"title":       h.cfg.AuthorName + "'s humanMCP",
		"description": h.cfg.AuthorBio,
		"version":     "0.1.0",
		"homepage":    "https://kapoost.github.io/humanmcp",
		"repository":  "https://github.com/kapoost/humanmcp",
		"remotes": []map[string]interface{}{
			{"type": "streamable-http", "url": "https://" + h.cfg.Domain + "/mcp"},
		},
		"tags": []string{"content", "publishing", "poetry", "intellectual-property", "personal", "creative"},
	})
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := h.store.Load(); err != nil {
		log.Printf("store load error: %v", err)
	}
	pieces := h.store.List(false)
	slugTags := make(map[string][]string)
	for _, p := range pieces {
		slugTags[p.Slug] = p.Tags
	}
	h.statStore.UpdateSlugTags(slugTags)

	blobMap := h.buildBlobImageMap()

	// v273 index.html expects pieces split by type into separate slices
	var poems, artworks []*content.Piece
	pieceSlugs := make(map[string]bool)
	for _, p := range pieces {
		// Populate FileRef from blob map for image/artwork pieces
		if url, ok := blobMap[p.Slug]; ok {
			p.FileRef = strings.TrimPrefix(url, "/")
		}
		// Populate Translations for the badge in index/template
		p.Translations = h.availableTranslations(p.Slug)
		pieceSlugs[p.Slug] = true
		switch strings.ToLower(string(p.Type)) {
		case "image":
			// Image-typed pieces fall through to gallery via blob iteration below
		case "artwork":
			artworks = append(artworks, p)
		default:
			poems = append(poems, p)
		}
	}

	// #images gallery: iterate image blobs (more authoritative than .md
	// pieces — v273 worked this way). Each blob becomes a tiny piece-like
	// struct for the template. Dedup by FileRef so the same file isn't
	// shown twice when blobs share an asset under different slugs.
	var images []*content.Piece
	seenFiles := make(map[string]bool)
	if blobs, err := h.blobStore.Load(); err == nil {
		for _, b := range blobs {
			if string(b.BlobType) != "image" || b.FileRef == "" {
				continue
			}
			if b.Access != content.AccessPublic {
				continue
			}
			if seenFiles[b.FileRef] {
				continue
			}
			seenFiles[b.FileRef] = true
			images = append(images, &content.Piece{
				Slug:      b.Slug,
				Title:     b.Title,
				Type:      "image",
				FileRef:   b.FileRef,
				Published: b.Published,
			})
		}
	}
	listings := h.listingStore.List()

	h.render(w, "index.html", map[string]interface{}{
		"Author":       h.cfg.AuthorName,
		"Bio":          h.cfg.AuthorBio,
		"Pieces":       pieces,
		"Poems":        poems,
		"Images":       images,
		"Artworks":     artworks,
		"Listings":     listings,
		"BlobImageMap": blobMap,
		"IsOwner":      h.auth.IsOwner(r),
		"Domain":       h.cfg.Domain,
	})
}

// pieceFromBlob looks up an image blob by slug and returns a synthetic
// Piece that piece.html can render via its existing image branch. Only
// public image-type blobs qualify — non-public blobs stay invisible to
// anonymous traffic. Returns (nil, false) when no matching blob exists.
func (h *Handler) pieceFromBlob(slug string) (*content.Piece, bool) {
	b, err := h.blobStore.Get(slug)
	if err != nil || b == nil {
		return nil, false
	}
	if string(b.BlobType) != "image" || b.FileRef == "" {
		return nil, false
	}
	if b.Access != content.AccessPublic && !h.auth.IsOwner(nil) {
		// Defensive — handler will re-check on a real request, but a
		// blob without public access shouldn't even appear here.
		return nil, false
	}
	return &content.Piece{
		Slug:      b.Slug,
		Title:     b.Title,
		Type:      "image",
		Access:    content.AccessLevel(b.Access),
		Body:      b.TextData, // optional caption
		Signature: b.Signature,
		Published: b.Published,
		Tags:      b.Tags,
		FileRef:   b.FileRef,
	}, true
}

// buildBlobImageMap returns slug -> URL for all image-type blobs.
// Used by templates that need {{index $.BlobImageMap .Slug}}.
func (h *Handler) buildBlobImageMap() map[string]string {
	m := make(map[string]string)
	blobs, err := h.blobStore.Load()
	if err != nil {
		return m
	}
	for _, b := range blobs {
		if b.FileRef != "" {
			m[b.Slug] = "/" + b.FileRef
		}
	}
	return m
}

func (h *Handler) handlePiece(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/p/")
	if path == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Translation route: /p/<slug>/translation/<lang>
	if parts := strings.SplitN(path, "/translation/", 2); len(parts) == 2 {
		if parts[0] == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		h.serveTranslation(w, r, parts[0], parts[1])
		return
	}
	// Edge case: /p/translation/<lang> with no slug — redirect to home
	if strings.HasPrefix(path, "translation/") {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	slug := path

	// If a translation exists and the user didn't explicitly ask for the
	// Polish-only view (?lang=pl) or login as owner — show the trilingual
	// page by default. Polish reader can still ?lang=pl out.
	if r.URL.Query().Get("lang") != "pl" && !h.auth.IsOwner(r) {
		if langs := h.availableTranslations(slug); len(langs) > 0 {
			h.serveTranslation(w, r, slug, langs[0])
			return
		}
	}
	if err := h.store.Load(); err != nil {
		log.Printf("store load error: %v", err)
	}

	isOwner := h.auth.IsOwner(r)
	p, err := h.store.Get(slug, isOwner)
	if err != nil {
		// Fallback: the slug might address an image blob, not a piece.
		// /images and the gallery section on / link image blobs via /p/<slug>
		// — without this fallback those links 404. Build a synthetic Piece
		// from the blob so piece.html's existing image branch can render
		// it with full meta (signature, license, ots).
		if syn, ok := h.pieceFromBlob(slug); ok {
			p = syn
		} else {
			http.NotFound(w, r)
			return
		}
	}

	if p.Access == content.AccessPublic && !isOwner {
		ua := r.Header.Get("User-Agent")
		ref := r.Header.Get("Referer")
		country := r.Header.Get("Fly-Region")
		if country == "" { country = r.Header.Get("X-Country") }
		ip := r.Header.Get("Fly-Client-IP")
		if ip == "" { ip = r.RemoteAddr }
		vh := content.VisitorHash(ip, time.Now().Format("2006-01-02"))
		h.statStore.Record(content.Event{
			Type:        content.EventRead,
			Caller:      content.CallerFromUA(ua),
			Slug:        slug,
			UA:          ua[:min(len(ua), 80)],
			Ref:         ref,
			Country:     country,
			VisitorHash: vh,
		})
	}

	isLocked := !p.IsUnlocked() && !isOwner
	var unlockDate string
	if p.Gate == content.GateTime && !p.UnlockAfter.IsZero() {
		unlockDate = p.UnlockAfter.Format("2 January 2006 at 15:04 UTC")
	}
	h.render(w, "piece.html", map[string]interface{}{
		"Author":       h.cfg.AuthorName,
		"Piece":        p,
		"IsLocked":     isLocked,
		"IsOwner":      isOwner,
		"UnlockDate":   unlockDate,
		"BlobImageMap": h.buildBlobImageMap(),
		"Translations": h.availableTranslations(slug),
	})
}

// availableTranslations returns language codes ("en", "es", …) for which a
// pre-rendered translation page exists in content/translations/.
func (h *Handler) availableTranslations(slug string) []string {
	dir := filepath.Join(h.cfg.ContentDir, "translations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := slug + "."
	var langs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".html") {
			continue
		}
		lang := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".html")
		if lang == "" || strings.Contains(lang, "/") || strings.Contains(lang, ".") {
			continue
		}
		langs = append(langs, lang)
	}
	return langs
}

// serveTranslation streams a pre-rendered translation HTML file.
// Path validation prevents directory traversal.
func (h *Handler) serveTranslation(w http.ResponseWriter, r *http.Request, slug, lang string) {
	if strings.ContainsAny(slug, "/.") || strings.ContainsAny(lang, "/.") || lang == "" || len(lang) > 5 {
		http.NotFound(w, r)
		return
	}
	file := filepath.Join(h.cfg.ContentDir, "translations", slug+"."+lang+".html")
	if _, err := os.Stat(file); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, file)
}

func (h *Handler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/unlock/")
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/p/"+slug, http.StatusFound)
		return
	}

	r.ParseForm()
	answer := r.FormValue("answer")

	p, err := h.store.Get(slug, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ua := r.Header.Get("User-Agent")
	ip := r.Header.Get("Fly-Client-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}
	vh := content.VisitorHash(ip, time.Now().Format("2006-01-02"))
	caller := content.CallerFromUA(ua)

	if h.store.CheckAnswer(slug, answer) {
		h.statStore.Record(content.Event{
			Type:        content.EventUnlock,
			Caller:      caller,
			Slug:        slug,
			Query:       answer, // record the successful answer
			VisitorHash: vh,
		})
		h.render(w, "piece.html", map[string]interface{}{
			"Author":   h.cfg.AuthorName,
			"Piece":    func() *content.Piece { p2, _ := h.store.Get(slug, true); return p2 }(),
			"IsLocked": false,
			"IsOwner":  false,
			"Unlocked": true,
		})
		return
	}

	// Wrong answer — log the attempt so owner can see what was tried
	h.statStore.Record(content.Event{
		Type:        content.EventUnlockFail,
		Caller:      caller,
		Slug:        slug,
		Query:       answer, // record the wrong answer attempted
		VisitorHash: vh,
	})
	h.render(w, "piece.html", map[string]interface{}{
		"Author":       h.cfg.AuthorName,
		"Piece":        p,
		"IsLocked":     true,
		"WrongAnswer":  true,
		"IsOwner":      false,
	})
}

// --- Owner API ---

func (h *Handler) handleAPIList(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Load(); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	pieces := h.store.List(true)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pieces)
}

func (h *Handler) handleAPIContent(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/content/")

	switch r.Method {
	case http.MethodGet:
		p, err := h.store.GetForEdit(slug)
		if err != nil {
			jsonError(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p)

	case http.MethodPut, http.MethodPost:
		// Use a raw map to handle flexible time fields from JS
		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			jsonError(w, "invalid json: "+err.Error(), 400)
			return
		}
		// Re-encode and decode into Piece
		data, _ := json.Marshal(raw)
		var p content.Piece
		json.Unmarshal(data, &p)
		if slug != "" && p.Slug == "" {
			p.Slug = slug
		}
		if p.Slug == "" {
			jsonError(w, "slug is required", 400)
			return
		}
		if p.Published.IsZero() {
			p.Published = time.Now()
		}
		// Auto-sign on save
		if h.signingKey != nil {
			if sig, err := content.SignPiece(&p, h.signingKey); err == nil {
				p.Signature = sig
			}
		}
		if err := h.store.Save(&p); err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "saved", "slug": p.Slug})

	case http.MethodDelete:
		if err := h.store.Delete(slug); err != nil {
			jsonError(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

// --- Login/logout ---


func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.statStore.Compute()
	if err != nil {
		http.Error(w, "stats error: "+err.Error(), 500)
		return
	}
	if err := h.store.Load(); err != nil {
		log.Printf("store load: %v", err)
	}
	pieces := h.store.List(false)
	msgs, _ := h.msgStore.List()
	listings := h.listingStore.List()

	now := time.Now()
	activePoem, _ := h.cfg.PickActivePoem(now)
	// SessionExp = top of the next hour. Matches PickActivePoem's
	// hourKey divisor (3600 seconds) — copy on the page must agree.
	sessionExp := now.Truncate(time.Hour).Add(time.Hour)

	view := h.buildEnrichedStats(stats, len(pieces), len(listings))

	h.render(w, "dashboard.html", map[string]interface{}{
		"Author":       h.cfg.AuthorName,
		"IsOwner":      true,
		"Stats":        view,
		"Pieces":       pieces,
		"Messages":     msgs,
		"Listings":     listings,
		"PieceCount":   len(pieces),
		"SessionCode":  activePoem,
		"SessionExp":   sessionExp,
		"SkillCount":   view.SkillCount,
		"PersonaCount": view.PersonaCount,
	})
}

func (h *Handler) countPersonas() int {
	dir := filepath.Join(h.cfg.ContentDir, "personas")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			count++
		}
	}
	return count
}

func (h *Handler) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "blob-uploader.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"IsOwner": true,
	})
}

func (h *Handler) handleAPIBlobs(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/blobs/")

	switch r.Method {
	case http.MethodGet:
		if slug == "" || slug == "/api/blobs" {
			blobs, _ := h.blobStore.Load()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(blobs)
			return
		}
		b, err := h.blobStore.Get(slug)
		if err != nil { jsonError(w, "not found", 404); return }
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(b)

	case http.MethodPost, http.MethodPut:
		// Multipart: supports file upload + metadata
		r.ParseMultipartForm(50 << 20) // 50MB
		var b content.Blob

		// Try JSON body first
		if r.Header.Get("Content-Type") == "application/json" {
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				jsonError(w, "invalid json", 400); return
			}
		} else {
			// Form fields
			b.Slug = r.FormValue("slug")
			b.Title = r.FormValue("title")
			b.BlobType = content.BlobType(r.FormValue("blob_type"))
			b.Description = r.FormValue("description")
			b.Access = content.AccessLevel(r.FormValue("access"))
			b.MimeType = r.FormValue("mime_type")
			b.Schema = r.FormValue("schema")
			b.Encoding = r.FormValue("encoding")
			b.TextData = r.FormValue("text_data")
			b.FileRef = r.FormValue("file_ref")  // preserve existing file reference
			if dim := r.FormValue("dimensions"); dim != "" {
				fmt.Sscanf(dim, "%d", &b.Dimensions)
			}
			if tags := r.FormValue("tags"); tags != "" {
				for _, t := range strings.Split(tags, ",") {
					b.Tags = append(b.Tags, strings.TrimSpace(t))
				}
			}

			// File upload
			if file, header, err := r.FormFile("file"); err == nil {
				defer file.Close()
				data := make([]byte, header.Size)
				file.Read(data)
				if b.MimeType == "" {
					b.MimeType = header.Header.Get("Content-Type")
				}
				ref, err := h.blobStore.StoreFile(b.Slug, header.Filename, data)
				if err != nil { jsonError(w, "file save error: "+err.Error(), 500); return }
				b.FileRef = ref
			}
		}

		if slug != "" && b.Slug == "" { b.Slug = slug }
		if b.Slug == "" { jsonError(w, "slug required", 400); return }

		// Auto-sign blob
		if h.signingKey != nil {
			if sig, err := content.SignBlob(&b, h.signingKey); err == nil {
				b.Signature = sig
			}
		}

		if err := h.blobStore.Save(&b); err != nil {
			jsonError(w, err.Error(), 500); return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "saved", "slug": b.Slug})

	case http.MethodDelete:
		if err := h.blobStore.Delete(slug); err != nil {
			jsonError(w, "not found", 404); return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

func (h *Handler) handleNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseMultipartForm(50 << 20)
		p := content.Piece{
			Slug:        slugify(r.FormValue("title") + " " + fmt.Sprintf("%d", time.Now().Unix())),
			Title:       r.FormValue("title"),
			Type:        r.FormValue("type"),
			Access:      content.AccessLevel(r.FormValue("access")),
			Gate:        content.GateType(r.FormValue("gate")),
			Challenge:   r.FormValue("challenge"),
			Answer:      r.FormValue("answer"),
			Description: r.FormValue("description"),
			Body:        r.FormValue("body"),
			Published:   time.Now(),
		}
		if r.FormValue("slug_override") != "" {
			p.Slug = r.FormValue("slug_override")
		} else if r.FormValue("slug") != "" {
			p.Slug = r.FormValue("slug")
		}
		if p.Type == "" { p.Type = "note" }
		p.License = r.FormValue("license")
		if ps := r.FormValue("price_sats"); ps != "" { fmt.Sscanf(ps, "%d", &p.PriceSats) }
		if tags := r.FormValue("tags"); tags != "" {
			for _, t := range strings.Split(tags, ",") {
				if s := strings.TrimSpace(t); s != "" {
					p.Tags = append(p.Tags, s)
				}
			}
		}
		if p.Title == "" { p.Title = firstLine(p.Body) }
		if h.signingKey != nil {
			if sig, err := content.SignPiece(&p, h.signingKey); err == nil {
				p.Signature = sig
			}
		}
		if err := h.store.Save(&p); err != nil {
			http.Error(w, err.Error(), 500); return
		}
		http.Redirect(w, r, "/p/"+p.Slug, http.StatusSeeOther)
		return
	}
	h.render(w, "new.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"Bio":     h.cfg.AuthorBio,
		"IsOwner": true,
	})
}

func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/edit/")
	if slug == "" { http.Redirect(w, r, "/new", http.StatusSeeOther); return }

	if r.Method == http.MethodPost {
		r.ParseMultipartForm(50 << 20)
		h.store.Load()
		p, err := h.store.GetForEdit(slug)
		if err != nil { http.Error(w, "not found", 404); return }
		p.Title       = r.FormValue("title")
		p.Type        = r.FormValue("type")
		p.Access      = content.AccessLevel(r.FormValue("access"))
		p.Gate        = content.GateType(r.FormValue("gate"))
		p.License      = r.FormValue("license")
		if ps := r.FormValue("price_sats"); ps != "" { fmt.Sscanf(ps, "%d", &p.PriceSats) }
		p.Challenge   = r.FormValue("challenge")
		p.Answer      = r.FormValue("answer")
		p.Description = r.FormValue("description")
		p.Body        = r.FormValue("body")
		p.Tags        = nil
		if tags := r.FormValue("tags"); tags != "" {
			for _, t := range strings.Split(tags, ",") {
				if s := strings.TrimSpace(t); s != "" {
					p.Tags = append(p.Tags, s)
				}
			}
		}
		if p.Title == "" { p.Title = firstLine(p.Body) }
		if h.signingKey != nil {
			if sig, err := content.SignPiece(p, h.signingKey); err == nil {
				p.Signature = sig
			}
		}
		if err := h.store.Save(p); err != nil {
			http.Error(w, err.Error(), 500); return
		}
		http.Redirect(w, r, "/p/"+slug, http.StatusSeeOther)
		return
	}

	h.store.Load()
	p, err := h.store.GetForEdit(slug)
	if err != nil { http.Error(w, "not found", 404); return }
	h.render(w, "new.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"Bio":     h.cfg.AuthorBio,
		"IsOwner": true,
		"Piece":   p,
	})
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Redirect(w, r, "/", http.StatusSeeOther); return }
	slug := strings.TrimPrefix(r.URL.Path, "/delete/")
	h.store.Load()
	h.store.Delete(slug)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleTimestamp stamps a piece into OpenTimestamps calendars (POST /timestamp/<slug>).
// Fresh piece (no OTSProof): submits the canonical sha256 to all default calendars
// and stores a base64-encoded .ots file in OTSProof.
// Already-pending piece: walks the proof, asks each calendar /timestamp/<commitment>
// for the Bitcoin attestation, and splices the upgraded path in.
func (h *Handler) handleTimestamp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/timestamp/")
	if slug == "" {
		http.Error(w, "missing slug", 400)
		return
	}
	h.store.Load()
	p, err := h.store.GetForEdit(slug)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	digest := content.PiecePayload(p)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if p.OTSProof == "" {
		// Fresh stamp — query every calendar in parallel, keep what answers.
		type result struct {
			body []byte
			err  error
		}
		results := make(chan result, len(defaultCalendars))
		for _, cal := range defaultCalendars {
			go func(cal string) {
				b, err := stampDigest(ctx, cal, digest)
				results <- result{b, err}
			}(cal)
		}
		var bodies [][]byte
		for range defaultCalendars {
			r := <-results
			if r.err == nil && len(r.body) > 0 {
				bodies = append(bodies, r.body)
			}
		}
		if len(bodies) == 0 {
			http.Error(w, "all calendars failed — try again later", 502)
			return
		}
		ots := buildOTSFile(digest, bodies)
		p.OTSProof = base64.StdEncoding.EncodeToString(ots)
		if h.signingKey != nil {
			if sig, err := content.SignPiece(p, h.signingKey); err == nil {
				p.Signature = sig
			}
		}
		if err := h.store.Save(p); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/p/"+slug, http.StatusSeeOther)
		return
	}

	// Upgrade flow: parse existing proof, walk pending attestations, fetch bitcoin paths.
	raw, err := base64.StdEncoding.DecodeString(p.OTSProof)
	if err != nil {
		http.Error(w, "stored proof not valid base64", 500)
		return
	}
	parsedDigest, body, err := splitOTS(raw)
	if err != nil {
		http.Error(w, "stored proof malformed: "+err.Error(), 500)
		return
	}
	upgraded, count, err := walkAndUpgrade(body, parsedDigest, func(cal string, commitment []byte) ([]byte, error) {
		return fetchUpgrade(ctx, cal, commitment)
	})
	if err != nil {
		http.Error(w, "upgrade walk failed: "+err.Error(), 500)
		return
	}
	if count == 0 {
		http.Redirect(w, r, "/p/"+slug+"?upgrade=not-ready", http.StatusSeeOther)
		return
	}
	newProof := buildOTSFile(parsedDigest, [][]byte{upgraded})
	p.OTSProof = base64.StdEncoding.EncodeToString(newProof)
	if h.signingKey != nil {
		if sig, err := content.SignPiece(p, h.signingKey); err == nil {
			p.Signature = sig
		}
	}
	if err := h.store.Save(p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/p/"+slug, http.StatusSeeOther)
}

// handleTimestampAll stamps every signed piece that doesn't yet have an OTSProof.
// Sequential to be friendly to calendar servers. Returns a JSON summary.
func (h *Handler) handleTimestampAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if err := h.store.Load(); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	pieces := h.store.List(true)

	type itemResult struct {
		Slug   string `json:"slug"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	results := []itemResult{}
	stamped, skipped, failed := 0, 0, 0

	for _, lp := range pieces {
		if lp.Signature == "" {
			results = append(results, itemResult{Slug: lp.Slug, Status: "skipped-unsigned"})
			skipped++
			continue
		}
		if lp.OTSProof != "" {
			results = append(results, itemResult{Slug: lp.Slug, Status: "skipped-already-stamped"})
			skipped++
			continue
		}

		p, err := h.store.GetForEdit(lp.Slug)
		if err != nil {
			results = append(results, itemResult{Slug: lp.Slug, Status: "error", Error: err.Error()})
			failed++
			continue
		}

		digest := content.PiecePayload(p)
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)

		type calResult struct {
			body []byte
			err  error
		}
		ch := make(chan calResult, len(defaultCalendars))
		for _, cal := range defaultCalendars {
			go func(cal string) {
				b, err := stampDigest(ctx, cal, digest)
				ch <- calResult{b, err}
			}(cal)
		}
		var bodies [][]byte
		for range defaultCalendars {
			r := <-ch
			if r.err == nil && len(r.body) > 0 {
				bodies = append(bodies, r.body)
			}
		}
		cancel()

		if len(bodies) == 0 {
			results = append(results, itemResult{Slug: lp.Slug, Status: "error", Error: "all calendars failed"})
			failed++
			continue
		}

		ots := buildOTSFile(digest, bodies)
		p.OTSProof = base64.StdEncoding.EncodeToString(ots)
		if h.signingKey != nil {
			if sig, err := content.SignPiece(p, h.signingKey); err == nil {
				p.Signature = sig
			}
		}
		if err := h.store.Save(p); err != nil {
			results = append(results, itemResult{Slug: lp.Slug, Status: "error", Error: err.Error()})
			failed++
			continue
		}
		results = append(results, itemResult{Slug: lp.Slug, Status: "stamped"})
		stamped++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stamped": stamped,
		"skipped": skipped,
		"failed":  failed,
		"items":   results,
	})
}

// slugify generates a URL-safe slug from a string
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prev := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = false
		} else if !prev {
			b.WriteRune('-')
			prev = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 50 { result = result[:50] }
	if result == "" { result = fmt.Sprintf("post-%d", time.Now().Unix()) }
	return result
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		return strings.TrimSpace(s[:idx])
	}
	if len(s) > 60 { return s[:60] }
	return strings.TrimSpace(s)
}

func (h *Handler) handleFile(w http.ResponseWriter, r *http.Request) {
	// /files/X → serve raw file from /data/blobs/files/X
	// Used by both blob-registered pieces AND listing images (which live
	// in blobs/files/ without a separate blob entry).
	slug := strings.TrimPrefix(r.URL.Path, "/files/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	dataDir := filepath.Dir(h.cfg.ContentDir)
	filePath := filepath.Join(dataDir, "blobs", "files", slug)
	if _, err := os.Stat(filePath); err != nil {
		http.NotFound(w, r)
		return
	}
	// If this file belongs to a registered blob, honour its access level.
	blobs, _ := h.blobStore.Load()
	for _, b := range blobs {
		if b.FileRef != "" && strings.HasSuffix(b.FileRef, slug) {
			if b.Access != content.AccessPublic {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if b.MimeType != "" {
				w.Header().Set("Content-Type", b.MimeType)
			}
			break
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filePath)
}

func (h *Handler) handleImages(w http.ResponseWriter, r *http.Request) {
	blobs, _ := h.blobStore.Load()
	var images []*content.Blob
	for _, b := range blobs {
		if b.BlobType == content.BlobImage && b.Access == content.AccessPublic {
			images = append(images, b)
		}
	}
	h.render(w, "images.html", map[string]interface{}{
		"Author": h.cfg.AuthorName,
		"Images": images,
		"Domain": h.cfg.Domain,
	})
}

func (h *Handler) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: https://%s/sitemap.xml\n", h.cfg.Domain)
}

func (h *Handler) handleSitemap(w http.ResponseWriter, r *http.Request) {
	h.store.Load()
	pieces := h.store.List(false)
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+`
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://%s/</loc></url>
  <url><loc>https://%s/connect</loc></url>
`, h.cfg.Domain, h.cfg.Domain)
	for _, p := range pieces {
		if p.Access == content.AccessPublic {
			fmt.Fprintf(w, "  <url><loc>https://%s/p/%s</loc><lastmod>%s</lastmod></url>\n",
				h.cfg.Domain, p.Slug, p.Published.Format("2006-01-02"))
		}
	}
	fmt.Fprintf(w, "</urlset>\n")
}

// handleAgentCard serves the A2A (Agent-to-Agent) discovery document.
// Spec reference: github.com/google/A2A. Capabilities advertise the
// dialogue + memory features that distinguish humanMCP from a generic
// MCP server; the MCP endpoint itself stays the source of truth for
// the tool list.
func (h *Handler) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	card := map[string]interface{}{
		"$schema":     "https://a2aproject.github.io/A2A/schemas/agent-card.json",
		"name":        h.cfg.AuthorName,
		"description": h.cfg.AuthorBio,
		"version":     "0.1.0",
		"url":         "https://" + h.cfg.Domain,
		"endpoints": map[string]interface{}{
			"mcp":     "https://" + h.cfg.Domain + "/mcp",
			"discovery": "https://" + h.cfg.Domain + "/.well-known/mcp-server.json",
		},
		"capabilities": []map[string]interface{}{
			{"name": "content", "description": "Public pieces (poems, essays) with Ed25519 signatures and optional OpenTimestamps anchoring."},
			{"name": "dialogue", "description": "ask_human / fetch_answer — submit a question, retrieve human-authored answer asynchronously."},
			{"name": "memory", "description": "remember / recall — persist agent observations across sessions, scoped to a session code."},
			{"name": "feedback", "description": "leave_comment / leave_message — surface reactions and messages to the author dashboard."},
			{"name": "licensing", "description": "request_license — declare intended use, receive terms, audit-logged."},
			{"name": "team", "description": "list_personas / get_persona / list_skills / get_skill — expert personas and instruction skills (post-session)."},
		},
		"contact": map[string]interface{}{
			"web":  "https://" + h.cfg.Domain + "/contact",
			"form": "https://" + h.cfg.Domain + "/connect",
		},
	}
	json.NewEncoder(w).Encode(card)
}

// handleRSS serves an RSS 2.0 feed of public pieces in reverse-chronological
// order. Used by feed readers and by the <link rel="alternate"> hint on the
// home page. Body of each piece is included raw — RSS readers handle
// plain-text fine; clients that want HTML can fetch /p/<slug>.
func (h *Handler) handleRSS(w http.ResponseWriter, r *http.Request) {
	h.store.Load()
	pieces := h.store.List(true)

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(w, `<rss version="2.0"><channel>`+"\n")
	fmt.Fprintf(w, "  <title>%s</title>\n", xmlEscape(h.cfg.AuthorName))
	fmt.Fprintf(w, "  <link>https://%s/</link>\n", h.cfg.Domain)
	fmt.Fprintf(w, "  <description>%s</description>\n", xmlEscape(h.cfg.AuthorBio))
	fmt.Fprintf(w, "  <language>pl</language>\n")
	for _, p := range pieces {
		if p.Access != content.AccessPublic {
			continue
		}
		fmt.Fprintf(w, "  <item>\n")
		fmt.Fprintf(w, "    <title>%s</title>\n", xmlEscape(p.Title))
		fmt.Fprintf(w, "    <link>https://%s/p/%s</link>\n", h.cfg.Domain, p.Slug)
		fmt.Fprintf(w, "    <guid isPermaLink=\"true\">https://%s/p/%s</guid>\n", h.cfg.Domain, p.Slug)
		fmt.Fprintf(w, "    <pubDate>%s</pubDate>\n", p.Published.UTC().Format(time.RFC1123Z))
		if p.Description != "" {
			fmt.Fprintf(w, "    <description>%s</description>\n", xmlEscape(p.Description))
		} else if p.Body != "" {
			fmt.Fprintf(w, "    <description>%s</description>\n", xmlEscape(p.Body))
		}
		fmt.Fprintf(w, "  </item>\n")
	}
	fmt.Fprintf(w, `</channel></rss>`+"\n")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	h.render(w, "connect.html", map[string]interface{}{
		"Author":    h.cfg.AuthorName,
		"Bio":       h.cfg.AuthorBio,
		"Domain":    h.cfg.Domain,
		"ToolCount": 14,
	})
}

func (h *Handler) handleContact(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// IP-based rate limit: 5 submissions per 10 minutes per IP.
		ip := h.contactClientIP(r)
		if !h.checkContactRateLimit(ip) {
			w.Header().Set("Retry-After", "600")
			h.render(w, "contact.html", map[string]interface{}{
				"Author":    h.cfg.AuthorName,
				"Error":     "Za dużo wiadomości z tego adresu w krótkim czasie. Spróbuj ponownie za ~10 minut.",
				"From":      r.FormValue("from"),
				"Text":      r.FormValue("text"),
				"Regarding": r.FormValue("regarding"),
			})
			return
		}
		r.ParseForm()
		from := r.FormValue("from")
		text := r.FormValue("text")
		regarding := r.FormValue("regarding")
		_, err := h.msgStore.Save(from, text, regarding)
		if err != nil {
			h.render(w, "contact.html", map[string]interface{}{
				"Author":    h.cfg.AuthorName,
				"Error":     err.Error(),
				"From":      from,
				"Text":      text,
				"Regarding": regarding,
			})
			return
		}
		h.statStore.Record(content.Event{
			Type:   content.EventMessage,
			Caller: content.CallerHuman,
			UA:     r.Header.Get("User-Agent"),
		})
		h.render(w, "contact.html", map[string]interface{}{
			"Author": h.cfg.AuthorName,
			"Sent":   true,
		})
		return
	}
	if err := h.store.Load(); err != nil {
		log.Printf("store load: %v", err)
	}
	pieces := h.store.List(false)
	// Pieces and listings deep-link to /contact?regarding=<slug> so the
	// submitted message lands tagged with the relevant content. The form
	// hidden field below picks this up.
	regarding := r.URL.Query().Get("regarding")
	h.render(w, "contact.html", map[string]interface{}{
		"Author":    h.cfg.AuthorName,
		"Pieces":    pieces,
		"Regarding": regarding,
	})
}

// contactClientIP returns the originating client IP for a /contact POST.
// Fly puts the real client IP in Fly-Client-IP; X-Forwarded-For is the
// fallback for other proxies; RemoteAddr is the last-resort source.
func (h *Handler) contactClientIP(r *http.Request) string {
	if ip := r.Header.Get("Fly-Client-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.SplitN(ip, ",", 2)[0]
	}
	return r.RemoteAddr
}

// checkContactRateLimit allows up to 5 POSTs per 10 minutes per IP for
// the anonymous /contact form. Generous to humans iterating on a message,
// tight enough to stop a script. Sliding window — recomputed on every
// call. Returns true if the request is allowed.
func (h *Handler) checkContactRateLimit(ip string) bool {
	const (
		windowSeconds = 600
		maxInWindow   = 5
	)
	h.contactRateMu.Lock()
	defer h.contactRateMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Duration(windowSeconds) * time.Second)
	var kept []time.Time
	for _, t := range h.contactRateLog[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxInWindow {
		h.contactRateLog[ip] = kept
		return false
	}
	kept = append(kept, now)
	h.contactRateLog[ip] = kept
	return true
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := h.msgStore.List()
	if err != nil {
		http.Error(w, "error loading messages: "+err.Error(), 500)
		return
	}
	h.render(w, "messages.html", map[string]interface{}{
		"Author":   h.cfg.AuthorName,
		"Messages": msgs,
		"IsOwner":  true,
	})
}

func (h *Handler) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/messages/")
	if id == "" {
		jsonError(w, "missing id", 400)
		return
	}
	if err := h.msgStore.Delete(id); err != nil {
		jsonError(w, err.Error(), 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		token := r.FormValue("token")
		if token == h.cfg.EditToken && token != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "edit_token",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		h.render(w, "login.html", map[string]interface{}{"Error": "Invalid token"})
		return
	}
	h.render(w, "login.html", nil)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "edit_token",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template error %s: %v", name, err)
		fmt.Fprintf(w, "template error: %v", err)
	}
}

// ── Skills API ───────────────────────────────────────────────────────────────

type apiSkill struct {
	Slug      string   `json:"slug"`
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	UpdatedBy string   `json:"updated_by,omitempty"`
}

func (h *Handler) loadSkills() []apiSkill {
	dir := filepath.Join(h.cfg.ContentDir, "skills")
	var out []apiSkill
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s apiSkill
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.Slug == "" {
			s.Slug = strings.TrimSuffix(e.Name(), ".json")
		}
		out = append(out, s)
	}
	return out
}

func (h *Handler) handleAPISkills(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	if slug == "/api/skills" {
		slug = ""
	}

	switch r.Method {
	case http.MethodGet:
		if slug == "" {
			skills := h.loadSkills()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(skills)
			return
		}
		// Get single skill
		skills := h.loadSkills()
		for _, s := range skills {
			if s.Slug == slug {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(s)
				return
			}
		}
		jsonError(w, "not found", 404)

	case http.MethodPost, http.MethodPut:
		var s apiSkill
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			jsonError(w, "invalid json: "+err.Error(), 400)
			return
		}
		if slug != "" && s.Slug == "" {
			s.Slug = slug
		}
		if s.Slug == "" {
			jsonError(w, "slug required", 400)
			return
		}
		dir := filepath.Join(h.cfg.ContentDir, "skills")
		os.MkdirAll(dir, 0755)
		data, _ := json.MarshalIndent(s, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, s.Slug+".json"), data, 0644); err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "saved", "slug": s.Slug})

	case http.MethodDelete:
		if slug == "" {
			jsonError(w, "slug required", 400)
			return
		}
		path := filepath.Join(h.cfg.ContentDir, "skills", slug+".json")
		if err := os.Remove(path); err != nil {
			jsonError(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", 405)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── Recovered v273 handlers ──────────────────────────────────────────────────

func (h *Handler) handleListings(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/listings" || path == "/listings/" {
		all := h.listingStore.List()
		showArchived := r.URL.Query().Get("archived") == "1"
		filtered := all[:0:0]
		archivedCount := 0
		for _, l := range all {
			if l.Status == "archived" {
				archivedCount++
				if !showArchived {
					continue
				}
			}
			filtered = append(filtered, l)
		}
		h.render(w, "listings.html", map[string]interface{}{
			"Author":        h.cfg.AuthorName,
			"IsOwner":       h.auth.IsOwner(r),
			"Listings":      filtered,
			"ShowArchived":  showArchived,
			"ArchivedCount": archivedCount,
		})
		return
	}
	if path == "/listings/new" {
		if !h.auth.IsOwner(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if r.Method == http.MethodPost {
			h.handleListingCreate(w, r)
			return
		}
		h.render(w, "listing-new.html", map[string]interface{}{
			"Author": h.cfg.AuthorName,
		})
		return
	}
	if strings.HasPrefix(path, "/listings/edit/") {
		if !h.auth.IsOwner(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		h.handleListingEdit(w, r, strings.TrimPrefix(path, "/listings/edit/"))
		return
	}
	if strings.HasPrefix(path, "/listings/delete/") {
		if !h.auth.IsOwner(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		h.handleListingDelete(w, r, strings.TrimPrefix(path, "/listings/delete/"))
		return
	}
	slug := strings.TrimPrefix(path, "/listings/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	listing, err := h.listingStore.Get(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.render(w, "listing.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"IsOwner": h.auth.IsOwner(r),
		"Listing": listing,
	})
}

func (h *Handler) handleListingEdit(w http.ResponseWriter, r *http.Request, slug string) {
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	listing, err := h.listingStore.Get(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		h.render(w, "listing-new.html", map[string]interface{}{
			"Author":  h.cfg.AuthorName,
			"IsOwner": true,
			"Listing": listing,
		})
		return
	}
	// Form is multipart/form-data because of the file input.
	if err := r.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		http.Error(w, err.Error(), 400)
		return
	}
	listing.Type = r.FormValue("type")
	listing.Title = strings.TrimSpace(r.FormValue("title"))
	listing.Body = r.FormValue("body")
	listing.Tags = splitTags(r.FormValue("tags"))
	listing.Price = r.FormValue("price")
	listing.PriceSats = r.FormValue("price_sats")
	if s := r.FormValue("status"); s != "" {
		listing.Status = s
	}
	if a := r.FormValue("access"); a != "" {
		listing.Access = a
	}
	if l := r.FormValue("lang"); l != "" {
		listing.Lang = l
	}
	if ea := r.FormValue("expires_at"); ea != "" {
		for _, layout := range []string{"2006-01-02T15:04", time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(ea)); err == nil {
				listing.ExpiresAt = t
				break
			}
		}
	}
	if r.FormValue("remove_image") == "1" {
		listing.ImageRef = ""
	}
	if ref, err := h.extractAndStoreImage(r, listing.Slug); err == nil && ref != "" {
		listing.ImageRef = ref
	}
	if err := h.listingStore.Save(*listing); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/listings/"+listing.Slug, http.StatusSeeOther)
}

func (h *Handler) handleListingDelete(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/listings", http.StatusSeeOther)
		return
	}
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	if err := h.listingStore.Delete(slug); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/listings", http.StatusSeeOther)
}

func (h *Handler) handleListingCreate(w http.ResponseWriter, r *http.Request) {
	// Form is multipart/form-data because of the file input; FormValue
	// lazily parses both shapes, but FormFile needs explicit parsing.
	if err := r.ParseMultipartForm(32 << 20); err != nil && err != http.ErrNotMultipart {
		http.Error(w, err.Error(), 400)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title required", 400)
		return
	}
	slugBase := slugify(title)
	listing := content.Listing{
		Slug:      fmt.Sprintf("%s-%d", slugBase, time.Now().Unix()),
		Type:      r.FormValue("type"),
		Title:     title,
		Body:      r.FormValue("body"),
		Tags:      splitTags(r.FormValue("tags")),
		Price:     r.FormValue("price"),
		Status:    "open",
		Access:    "public",
		Published: time.Now().UTC(),
		Lang:      r.FormValue("lang"),
	}
	if ref, err := h.extractAndStoreImage(r, listing.Slug); err == nil && ref != "" {
		listing.ImageRef = ref
	}
	if err := h.listingStore.Save(listing); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/listings/"+listing.Slug, http.StatusFound)
}

// extractAndStoreImage reads the "image" multipart file (if any) and
// persists it to the blob store under the listing's slug. Returns the
// ImageRef string (relative path like "files/abc.jpg") or an empty
// string when no file was submitted.
func (h *Handler) extractAndStoreImage(r *http.Request, slug string) (string, error) {
	file, header, err := r.FormFile("image")
	if err != nil {
		if err == http.ErrMissingFile {
			return "", nil
		}
		return "", err
	}
	defer file.Close()
	if header == nil || header.Size == 0 {
		return "", nil
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return h.blobStore.StoreFile(slug, header.Filename, data)
}

func (h *Handler) handleArtworks(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/artworks" || path == "/artworks/" {
		var artworks []*content.Piece
		for _, p := range h.store.List(false) {
			if strings.EqualFold(string(p.Type), "artwork") {
				artworks = append(artworks, p)
			}
		}
		h.render(w, "artworks.html", map[string]interface{}{
			"Author":   h.cfg.AuthorName,
			"IsOwner":  h.auth.IsOwner(r),
			"Artworks": artworks,
		})
		return
	}
	slug := strings.TrimPrefix(path, "/artworks/")
	p, err := h.store.Get(slug, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.EqualFold(string(p.Type), "artwork") {
		http.NotFound(w, r)
		return
	}
	h.render(w, "artwork.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"IsOwner": h.auth.IsOwner(r),
		"Piece":   p,
	})
}

// formatUptime renders a duration as "3d 4h" / "5h 12m" / "47s".
func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

// periodStats is daily/weekly aggregate used by mc.html and dashboard.html.
type periodStats struct {
	Reads, Visitors, Agents, Humans, Searches, Messages, Licenses int
}

// enrichedStats embeds *content.Stats and adds the extra fields v273
// templates (mc.html, dashboard.html) expect inside {{with .Stats}}...
type enrichedStats struct {
	*content.Stats
	PieceCount         int
	SkillCount         int
	PersonaCount       int
	TotalListings      int
	TotalLicenses      int
	TotalSearches      int
	TotalSubscribers   int
	Today              periodStats
	Yesterday          periodStats
	Last7Days          periodStats
	Last30Days         periodStats
	DailyCounts        []int
	ListingReadsBySlug map[string]int
	InboxCount         int
	InboxCounts        map[string]int
	Inbox              []interface{}
	TopSearches        map[string]int
	SessionExp         time.Time
	Uptime             string
	VaultOnline        bool
	ToolCalls          int
}

func (h *Handler) buildEnrichedStats(stats *content.Stats, pieceCount, listingCount int) enrichedStats {
	// Drop stale slugs from ChallengeFunnel and AttemptsBySlug — pieces
	// can be deleted but their historical events stay in stats.ndjson
	// forever, polluting the dashboard with ghost entries.
	live := h.liveSlugs()
	if len(stats.ChallengeFunnel) > 0 {
		filtered := make(map[string][3]int, len(stats.ChallengeFunnel))
		for slug, f := range stats.ChallengeFunnel {
			if live[slug] {
				filtered[slug] = f
			}
		}
		stats.ChallengeFunnel = filtered
	}
	if len(stats.AttemptsBySlug) > 0 {
		filtered := make(map[string][]content.Event, len(stats.AttemptsBySlug))
		for slug, attempts := range stats.AttemptsBySlug {
			if live[slug] {
				filtered[slug] = attempts
			}
		}
		stats.AttemptsBySlug = filtered
	}
	es := enrichedStats{
		Stats:         stats,
		PieceCount:    pieceCount,
		SkillCount:    len(h.loadSkills()),
		PersonaCount:  h.countPersonas(),
		TotalListings: listingCount,
		VaultOnline:   true,
		Uptime:        formatUptime(time.Since(h.startedAt)),
	}
	if w, err := h.statStore.ComputeWindows(time.Now()); err == nil && w != nil {
		toPS := func(ws content.WindowStats) periodStats {
			return periodStats{
				Reads:    ws.Reads,
				Visitors: ws.Visitors,
				Agents:   ws.Agents,
				Humans:   ws.Humans,
				Messages: ws.Messages,
			}
		}
		es.Today = toPS(w.Today)
		es.Yesterday = toPS(w.Yesterday)
		es.Last7Days = toPS(w.Last7Days)
		es.Last30Days = toPS(w.Last30Days)
		es.DailyCounts = w.DailyReads[:]
	}
	return es
}

// liveSlugs returns the set of slugs currently backed by a piece or listing.
// Used to filter out stats entries for deleted content.
func (h *Handler) liveSlugs() map[string]bool {
	live := make(map[string]bool)
	for _, p := range h.store.List(false) {
		live[p.Slug] = true
	}
	for _, l := range h.listingStore.List() {
		live[l.Slug] = true
	}
	return live
}

// inboxItem is what mc.html's {{range .Inbox}} iterates over.
// Either M (a Message) or Q (a Question) is set, dispatched via Kind.
type inboxItem struct {
	Kind string            // "msg" | "q-pending" | "q-awaiting" | "q-picked"
	M    *content.Message  // when Kind=="msg"
	Q    *content.Question // when Kind starts with "q-"
}

func (h *Handler) handleMissionControl(w http.ResponseWriter, r *http.Request) {
	if !h.auth.IsOwner(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	stats, err := h.statStore.Compute()
	if err != nil {
		http.Error(w, "stats error: "+err.Error(), 500)
		return
	}
	if err := h.store.Load(); err != nil {
		log.Printf("store load: %v", err)
	}
	pieces := h.store.List(false)
	msgs, _ := h.msgStore.List()
	listings := h.listingStore.List()
	questions := h.questionStore.List()
	now := time.Now()
	activePoem, _ := h.cfg.PickActivePoem(now)

	// Build unified Inbox: messages + pending questions + awaiting-pickup +
	// recently-picked. mc.html dispatches on .Kind.
	var inbox []inboxItem
	msgCount := 0
	pendingCount := 0
	awaitingCount := 0
	pickedCount := 0
	for _, m := range msgs {
		inbox = append(inbox, inboxItem{Kind: "msg", M: m})
		msgCount++
	}
	for i := range questions {
		q := questions[i]
		switch {
		case q.IsAwaiting():
			inbox = append(inbox, inboxItem{Kind: "q-pending", Q: &q})
			pendingCount++
		case q.IsPicked():
			inbox = append(inbox, inboxItem{Kind: "q-awaiting", Q: &q})
			awaitingCount++
		default:
			inbox = append(inbox, inboxItem{Kind: "q-picked", Q: &q})
			pickedCount++
		}
	}

	view := h.buildEnrichedStats(stats, len(pieces), len(listings))
	view.Inbox = make([]interface{}, len(inbox))
	for i, it := range inbox {
		view.Inbox[i] = it
	}
	view.InboxCount = len(inbox)
	view.InboxCounts = map[string]int{
		"msg":      msgCount,
		"pending":  pendingCount,
		"awaiting": awaitingCount,
		"picked":   pickedCount,
	}

	sessionExp := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, now.Location())

	h.render(w, "mc.html", map[string]interface{}{
		"Author":      h.cfg.AuthorName,
		"IsOwner":     true,
		"Stats":       view,
		"Pieces":      pieces,
		"Messages":    msgs,
		"Listings":    listings,
		"Questions":   questions,
		"Inbox":       view.Inbox,
		"InboxCounts": view.InboxCounts,
		"SessionCode": activePoem,
		"SessionExp":  sessionExp,
		"VaultOnline": true,
		"Uptime":      view.Uptime,
		"ToolCalls":   view.ToolCalls,
	})
}

func (h *Handler) handleTeam(w http.ResponseWriter, r *http.Request) {
	personas := h.loadPersonasList()
	h.render(w, "team.html", map[string]interface{}{
		"Author":   h.cfg.AuthorName,
		"Personas": personas,
	})
}

func (h *Handler) handlePersonasPage(w http.ResponseWriter, r *http.Request) {
	personas := h.loadPersonasList()
	h.render(w, "personas.html", map[string]interface{}{
		"Author":   h.cfg.AuthorName,
		"IsOwner":  h.auth.IsOwner(r),
		"Personas": personas,
	})
}

func (h *Handler) handleSkillsPage(w http.ResponseWriter, r *http.Request) {
	skills := h.loadSkills()
	type skillGroup struct {
		Name   string
		Skills []apiSkill
	}
	byCat := map[string]*skillGroup{}
	var order []string
	for _, s := range skills {
		g, ok := byCat[s.Category]
		if !ok {
			g = &skillGroup{Name: s.Category}
			byCat[s.Category] = g
			order = append(order, s.Category)
		}
		g.Skills = append(g.Skills, s)
	}
	groups := make([]skillGroup, 0, len(order))
	for _, k := range order {
		groups = append(groups, *byCat[k])
	}
	h.render(w, "skills.html", map[string]interface{}{
		"Author":  h.cfg.AuthorName,
		"IsOwner": h.auth.IsOwner(r),
		"Skills":  skills,
		"Groups":  groups,
	})
}

func (h *Handler) handleQuestions(w http.ResponseWriter, r *http.Request) {
	if !h.auth.IsOwner(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	all := h.questionStore.List()
	var pending, picked, awaiting []content.Question
	for _, q := range all {
		switch {
		case q.IsAwaiting():
			awaiting = append(awaiting, q)
		case q.IsPicked():
			picked = append(picked, q)
		default:
			pending = append(pending, q)
		}
	}
	h.render(w, "questions.html", map[string]interface{}{
		"Author":   h.cfg.AuthorName,
		"Awaiting": awaiting,
		"Picked":   picked,
		"Pending":  pending,
	})
}

func (h *Handler) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	if !h.auth.IsOwner(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Templates send id via hidden form field; legacy routes embed it in
	// the URL path. Accept either, prefer form (more explicit).
	id := strings.TrimSpace(r.FormValue("question_id"))
	if id == "" {
		id = strings.TrimPrefix(r.URL.Path, "/questions/answer/")
	}
	if id == "" {
		http.Error(w, "id required", 400)
		return
	}
	answer := strings.TrimSpace(r.FormValue("answer"))
	if answer == "" {
		http.Error(w, "answer required", 400)
		return
	}
	if err := h.questionStore.Answer(id, answer); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/questions", http.StatusFound)
}

func (h *Handler) handleForAgents(w http.ResponseWriter, r *http.Request) {
	h.render(w, "for-agents.html", map[string]interface{}{
		"Author": h.cfg.AuthorName,
		"Domain": h.cfg.Domain,
	})
}

func (h *Handler) handleSubscribeForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "subscribe.html", map[string]interface{}{
		"Author": h.cfg.AuthorName,
	})
}

func (h *Handler) handleSubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	// POST = persist a new subscription. GET = the legacy "thanks" page;
	// without a record id it has nothing meaningful to confirm, so we
	// redirect back to the form.
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/subscribe", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	sub := content.Subscription{
		Channel:     strings.TrimSpace(r.FormValue("channel")),
		CallbackURL: strings.TrimSpace(r.FormValue("callback_url")),
		FilterTypes: r.Form["filter_types"],
		FilterTags:  splitTags(r.FormValue("filter_tags")),
	}
	saved, err := h.subscriptionStore.Save(sub)
	if err != nil {
		h.render(w, "subscribe.html", map[string]interface{}{
			"Author": h.cfg.AuthorName,
			"Error":  err.Error(),
		})
		return
	}
	h.statStore.Record(content.Event{
		Type:   content.EventMessage,
		Caller: content.CallerHuman,
		UA:     r.Header.Get("User-Agent"),
	})
	h.render(w, "subscribe-confirm.html", map[string]interface{}{
		"Author":       h.cfg.AuthorName,
		"Domain":       h.cfg.Domain,
		"Subscription": saved,
	})
}

func (h *Handler) loadPersonasList() []map[string]interface{} {
	dir := filepath.Join(h.cfg.ContentDir, "personas")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		body := string(data)
		var title, role, prompt string
		var tags []string
		if strings.HasPrefix(body, "---\n") {
			end := strings.Index(body[4:], "\n---")
			if end > 0 {
				front := body[4 : 4+end]
				prompt = strings.TrimSpace(body[4+end+4:])
				for _, line := range strings.Split(front, "\n") {
					k, v, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					k = strings.TrimSpace(strings.ToLower(k))
					v = strings.TrimSpace(v)
					switch k {
					case "title":
						title = v
					case "role":
						role = v
					case "tags":
						v = strings.Trim(v, "[]")
						for _, t := range strings.Split(v, ",") {
							t = strings.TrimSpace(t)
							if t != "" {
								tags = append(tags, t)
							}
						}
					}
				}
			}
		}
		if title == "" {
			title = slug
		}
		out = append(out, map[string]interface{}{
			"Slug":   slug,
			"Name":   title,
			"Role":   role,
			"Tags":   tags,
			"Prompt": prompt,
		})
	}
	return out
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// /stats and /gallery — aliases for /dashboard and /images respectively
func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) handleGallery(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/images", http.StatusFound)
}

// /llms.txt — plain text catalogue for AI agents
func (h *Handler) handleLLMSTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	dataDir := filepath.Dir(h.cfg.ContentDir)
	custom := filepath.Join(dataDir, "llms.txt")
	if data, err := os.ReadFile(custom); err == nil {
		w.Write(data)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", h.cfg.AuthorName, h.cfg.AuthorBio)
	fmt.Fprintf(&b, "> Personal humanMCP server. Connect via https://%s/mcp\n\n", h.cfg.Domain)
	fmt.Fprintln(&b, "## Pieces")
	fmt.Fprintln(&b)
	for _, p := range h.store.List(false) {
		fmt.Fprintf(&b, "- [%s](https://%s/p/%s)", p.Title, h.cfg.Domain, p.Slug)
		if p.Description != "" {
			fmt.Fprintf(&b, ": %s", p.Description)
		}
		fmt.Fprintln(&b)
	}
	w.Write([]byte(b.String()))
}

// /llms-edit — owner editor for /llms.txt
func (h *Handler) handleLLMSTxtEdit(w http.ResponseWriter, r *http.Request) {
	dataDir := filepath.Dir(h.cfg.ContentDir)
	custom := filepath.Join(dataDir, "llms.txt")
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		body := r.FormValue("body")
		if err := os.WriteFile(custom, []byte(body), 0o644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/llms.txt", http.StatusFound)
		return
	}
	body := ""
	if data, err := os.ReadFile(custom); err == nil {
		body = string(data)
	}
	h.render(w, "llms-edit.html", map[string]interface{}{
		"Author": h.cfg.AuthorName,
		"Domain": h.cfg.Domain,
		"Body":   body,
	})
}
