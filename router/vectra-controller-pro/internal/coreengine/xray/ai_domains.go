package xray

// AIDomains are the AI services' domains the service «Нейросети» (ID "ai")
// routes besides the provider's own AI rule: the services that refuse
// Russian users, by their own name only — never a whole platform (all of
// Google, Microsoft or X stay where they are), only the AI products' hosts.
var AIDomains = []string{
	// OpenAI / ChatGPT / Sora
	"domain:openai.com", "domain:chatgpt.com", "domain:chat.com", "domain:oaistatic.com",
	"domain:oaiusercontent.com", "domain:sora.com",
	// Anthropic / Claude
	"domain:anthropic.com", "domain:claude.ai", "domain:claude.com", "domain:claudeusercontent.com",
	// Google Gemini, AI Studio, NotebookLM, Labs
	"full:gemini.google.com", "domain:gemini.google", "full:aistudio.google.com", "full:bard.google.com",
	"full:notebooklm.google.com", "domain:notebooklm.google", "full:makersuite.google.com",
	"full:generativelanguage.googleapis.com", "full:alkalimakersuite-pa.clients6.google.com",
	"full:proactivebackend-pa.googleapis.com", "full:robinfrontend-pa.googleapis.com",
	"domain:labs.google", "full:ai.google.dev", "domain:deepmind.com", "domain:deepmind.google",
	// Microsoft Copilot, GitHub Copilot
	"full:copilot.microsoft.com", "domain:copilot.cloud.microsoft", "full:sydney.bing.com",
	"domain:githubcopilot.com", "full:copilot-proxy.githubusercontent.com",
	// xAI Grok
	"domain:x.ai", "domain:grok.com",
	// Perplexity, Mistral, Meta AI, Poe, Character.AI
	"domain:perplexity.ai", "domain:pplx.ai", "domain:mistral.ai", "domain:meta.ai",
	"domain:poe.com", "domain:poecdn.net", "domain:character.ai",
	// Images, video, audio
	"domain:midjourney.com", "domain:runwayml.com", "domain:pika.art", "domain:ideogram.ai",
	"domain:leonardo.ai", "domain:suno.com", "domain:suno.ai", "domain:udio.com", "domain:elevenlabs.io",
	// Code assistants
	"domain:cursor.com", "domain:cursor.sh", "domain:cursorapi.com", "domain:codeium.com", "domain:windsurf.com",
	// Translation, model hubs
	"domain:deepl.com", "domain:openrouter.ai", "domain:groq.com", "domain:replicate.com",
}
