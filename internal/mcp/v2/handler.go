// Package v2 is the MCP 2026-07-28 handler built on the official go-sdk.
// It runs alongside the legacy internal/mcp Handler during the migration
// window: /mcp keeps 2024-11-05 semantics, /mcp/v2 speaks stateless
// Streamable HTTP with the new spec.
//
// v2 reuses state (personas, skills, stores, config) from the legacy handler
// via exported accessors so a single Handler instance still owns loading
// and caching. Tool bodies are duplicated on purpose — parity with v1 is
// asserted by storyboards, not by shared render helpers.
package v2

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kapoost/humanmcp-go/internal/config"
	"github.com/kapoost/humanmcp-go/internal/content"
	"github.com/kapoost/humanmcp-go/internal/mcp"
	"github.com/kapoost/humanmcp-go/internal/mysloodsiewnia"
)

// Source is what v2 needs from the legacy handler. Kept narrow so v2 stays
// decoupled from wire-layer methods that are being phased out.
type Source interface {
	LoadPersonas() []mcp.Persona
	LoadSkills() []mcp.Skill
	Config() *config.Config
	Store() *content.Store
	StatStore() *content.StatStore
	ProvenanceStore() *content.ProvenanceStore
	CollectionStore() *content.CollectionStore
	BlobStore() *content.BlobStore
	MsgStore() *content.MessageStore
	MemoryStore() *content.MemoryStore
	IsSessionActiveByHeaders(http.Header) bool
	IsOwnerRequestByHeaders(http.Header) bool
	ValidateSessionCode(string) bool
	ClientIPFromHeaders(http.Header) string
	CheckBootstrapRateLimit(string) bool

	// rituals + dialogue
	QuestionStore() *content.QuestionStore
	RitualStore() *content.RitualStore
	PersonaJournalStore() *content.PersonaJournalStore
	LLMAvailable() bool
	CheckAskHumanRateLimit(string) bool
	CheckFetchAnswerRateLimit(string) bool
	CheckNaradaRateLimit(string) bool
	CheckNaradaFetchRateLimit(string) bool
	CreateNaradaJob(context, from string) (content.RitualJob, []string, error)
	WriteReflection(naradaID, personaSlug, errorContext string) (string, error)
	SynthesisePersonaPatternsBySlug(slug string) (content.PersonaPatterns, int, error)

	// mysłoodsiewnia bridge. Both may be nil if the bridge is disabled —
	// tools handle that as StatusUnreachable, not as a panic.
	Liveness() *mysloodsiewnia.Liveness
	BridgeQueue() *mysloodsiewnia.Queue
}

// New wires an SDK server + StreamableHTTPHandler for path-based mounting.
// Stateless: true is mandatory for 2026-07-28 per the SDK v1.7.0 release notes.
func New(cfg *config.Config, src Source) http.Handler {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "humanMCP — kapoost",
		Version: "0.2.0-dev",
	}, nil)

	// discovery
	registerAboutHumanmcp(server, cfg)
	registerGetAuthorProfile(server, src)
	registerListContent(server, src)
	registerListPersonas(server, src)
	registerListSkills(server, src)

	// content reads
	registerReadContent(server, src)
	registerVerifyContent(server, src)
	registerGetCertificate(server, src)

	// provenance
	registerListProvenance(server, src)
	registerReadProvenance(server, src)

	// collections
	registerListCollection(server, src)
	registerReadCollectionItem(server, src)

	// blobs
	registerListBlobs(server, src)
	registerReadBlob(server, src)

	// skill groups
	registerListSkillGroups(server, src)
	registerLoadSkillGroup(server, src)
	registerSuggestSkills(server, src)

	// feedback
	registerLeaveComment(server, src)
	registerLeaveMessage(server, src)

	// access
	registerRequestAccess(server, src)
	registerSubmitAnswer(server, src)
	registerRequestLicense(server, src)

	// memory
	registerRemember(server, src)
	registerRecall(server, src)

	// editing (owner-only)
	registerUpsertSkill(server, src)
	registerDeleteSkill(server, src)

	// team
	registerBootstrapSession(server, src)
	registerGetPersona(server, src)
	registerGetSkill(server, src)

	// dialogue
	registerAskHuman(server, src)
	registerFetchAnswer(server, src)

	// rituals
	registerRunNarada(server, src)
	registerFetchNaradaResult(server, src)
	registerGetPersonaJournal(server, src)
	registerRecordPersonaReflection(server, src)
	registerSynthesisePersonaPatterns(server, src)

	// mysłoodsiewnia bridge (read-only wave 1)
	registerMysloodsiewniaStatus(server, src)
	registerMysloodsiewniaSearch(server, src)
	registerMysloodsiewniaGet(server, src)

	return sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return server
	}, &sdk.StreamableHTTPOptions{Stateless: true})
}

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: text}},
	}
}
