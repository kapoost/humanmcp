package v2

// ToolNames returns every MCP tool this handler registers, in the order
// they're added in New(). Pinned as a static list so:
//
//   - the initialize instructions block can bake the count without
//     round-tripping through the SDK server;
//   - internal/web/phantom_tools_test can compare docs against reality
//     without instantiating a full server;
//   - the count-drift regression the docs test guards remains fast.
//
// Adding a tool: register it in the appropriate v2/<family>.go file AND
// append the name here. The order matches New() for reviewer sanity.
func ToolNames() []string {
	return []string{
		// discovery
		"about_humanmcp",
		"get_author_profile",
		"list_content",
		"list_personas",
		"list_skills",
		// content reads
		"read_content",
		"verify_content",
		"get_certificate",
		// provenance
		"list_provenance",
		"read_provenance",
		// collections
		"list_collection",
		"read_collection_item",
		// blobs
		"list_blobs",
		"read_blob",
		// skill groups
		"list_skill_groups",
		"load_skill_group",
		"suggest_skills",
		// feedback
		"leave_comment",
		"leave_message",
		// access
		"request_access",
		"submit_answer",
		"request_license",
		// memory
		"remember",
		"recall",
		// editing (owner-only)
		"upsert_skill",
		"delete_skill",
		// team
		"bootstrap_session",
		"get_persona",
		"get_skill",
		// dialogue
		"ask_human",
		"fetch_answer",
		// rituals
		"run_narada",
		"fetch_narada_result",
		"get_persona_journal",
		"record_persona_reflection",
		"synthesise_persona_patterns",
		// mysłoodsiewnia bridge
		"mysloodsiewnia_status",
		"mysloodsiewnia_search",
		"mysloodsiewnia_get",
		"mysloodsiewnia_list",
		"mysloodsiewnia_write",
	}
}
