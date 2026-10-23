package catalog

// Input schemas (JSON Schema, draft 2020-12) of the capabilities that the
// organisation's agent may call for a member. The node serves them to the
// agent as tool definitions (anet turn-mcp); the service still checks every
// argument itself, so a schema describes and never widens what is accepted.
// Lengths are in bytes on the service side, so they are stated in the
// descriptions rather than as maxLength (which counts characters).

const (
	idProp      = `{"type": "integer", "minimum": 1, "description": "The board item id."}`
	noArguments = `{"type": "object", "properties": {}, "additionalProperties": false}`
)

var inputSchemas = map[string]string{
	"org.info": noArguments,
	"org.me":   noArguments,
	"org.members": `{"type": "object", "additionalProperties": false, "properties": {
		"query": {"type": "string", "description": "Part of a handle to search for."},
		"kind": {"type": "string", "enum": ["student", "service", "staff"]},
		"include_aid": {"type": "boolean", "description": "Also return each member's AID."},
		"limit": {"type": "integer", "minimum": 1, "maximum": 500},
		"offset": {"type": "integer", "minimum": 0, "maximum": 100000}}}`,

	"msg.send": `{"type": "object", "additionalProperties": false, "required": ["to", "text"], "properties": {
		"to": {"type": "string", "description": "A member's handle (with or without @), or the group staff; the group all is open to staff only."},
		"text": {"type": "string", "description": "The message, at most 4000 bytes."}}}`,
	"msg.inbox": `{"type": "object", "additionalProperties": false, "properties": {
		"since": {"type": ["integer", "string"], "description": "Only messages after this message id, or sent at or after this RFC 3339 time."},
		"limit": {"type": "integer", "minimum": 1, "maximum": 200}}}`,
	"msg.ack": `{"type": "object", "additionalProperties": false, "properties": {
		"ids": {"type": "array", "items": {"type": "integer"}, "maxItems": 500, "description": "Message ids to remove from the mailbox."},
		"up_to": {"type": "integer", "minimum": 1, "description": "Remove every message up to and including this id."}}}`,

	"board.post": `{"type": "object", "additionalProperties": false, "required": ["title"], "properties": {
		"title": {"type": "string", "description": "What is needed, at most 200 bytes."},
		"body": {"type": "string", "description": "The details: what, by when, what the helper gets; at most 4000 bytes."},
		"tags": {"type": "array", "maxItems": 8, "items": {"type": "string", "pattern": "^[a-z0-9][a-z0-9_.-]{0,31}$"},
			"description": "The skills needed, for example cuda or statistics, and help-wanted for a request for help."}}}`,
	"board.list": `{"type": "object", "additionalProperties": false, "properties": {
		"id": {"type": "integer", "minimum": 1, "description": "One item, with its comments and its claimer."},
		"status": {"type": "string", "enum": ["active", "open", "claimed", "done", "withdrawn", "all"], "description": "Default active: open and claimed."},
		"tag": {"type": "string"},
		"mine": {"type": "boolean", "description": "Only items the member posted or claimed."},
		"query": {"type": "string", "description": "Words in the title or body."},
		"limit": {"type": "integer", "minimum": 1, "maximum": 100},
		"before": {"type": "integer", "minimum": 1, "description": "Only items with a smaller id (the next page)."}}}`,
	"board.claim": `{"type": "object", "additionalProperties": false, "required": ["id"], "properties": {
		"id": ` + idProp + `,
		"release": {"type": "boolean", "description": "Give up the member's claim instead."}}}`,
	"board.done": `{"type": "object", "additionalProperties": false, "required": ["id"], "properties": {
		"id": ` + idProp + `,
		"note": {"type": "string", "description": "The outcome, at most 1000 bytes."},
		"withdraw": {"type": "boolean", "description": "Withdraw the item instead (its author only)."}}}`,
	"board.comment": `{"type": "object", "additionalProperties": false, "required": ["id", "text"], "properties": {
		"id": ` + idProp + `,
		"text": {"type": "string", "description": "The comment, at most 2000 bytes."}}}`,
	"board.delete": `{"type": "object", "additionalProperties": false, "required": ["id"], "properties": {
		"id": ` + idProp + `}}`,

	"research.project.create": `{"type": "object", "additionalProperties": false, "required": ["name"], "properties": {
		"name": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{1,39}$"},
		"title": {"type": "string", "description": "At most 200 bytes."},
		"description": {"type": "string", "description": "At most 2000 bytes."}}}`,
	"research.project.list": `{"type": "object", "additionalProperties": false, "properties": {
		"query": {"type": "string"},
		"limit": {"type": "integer", "minimum": 1, "maximum": 200}}}`,
	"research.note.add": `{"type": "object", "additionalProperties": false, "required": ["project", "text"], "properties": {
		"project": {"type": "string", "description": "The project name."},
		"text": {"type": "string", "description": "At most 12000 bytes."},
		"kind": {"type": "string", "enum": ["note", "reading", "minutes"]},
		"title": {"type": "string", "description": "At most 200 bytes."},
		"url": {"type": "string", "description": "An http or https URL."}}}`,
	"research.note.list": `{"type": "object", "additionalProperties": false, "properties": {
		"project": {"type": "string", "description": "The project name; or leave it out with mine for the member's notes in every project."},
		"kind": {"type": "string", "enum": ["note", "reading", "minutes"]},
		"mine": {"type": "boolean"},
		"limit": {"type": "integer", "minimum": 1, "maximum": 100},
		"before": {"type": "integer", "minimum": 1}}}`,
	"research.note.delete": `{"type": "object", "additionalProperties": false, "required": ["id"], "properties": {
		"id": {"type": "integer", "minimum": 1, "description": "The note id."}}}`,
}
