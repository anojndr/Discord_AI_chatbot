# DiscordAIChatbot

A high-performance Go implementation of llmcord, recoded from the original Python version at https://github.com/anojndr/llmcord.

## Talk to LLMs with your friends!

![image](https://github.com/user-attachments/assets/7791cc6b-6755-484f-a9e3-0707765b081f)

DiscordAIChatbot transforms Discord into a collaborative LLM frontend. It works with practically any LLM, remote or locally hosted.

## Features

### Reply-based chat system:
Just @ the bot or say "at ai" to start a conversation and reply to continue. Build conversations with reply chains!

You can:
- Branch conversations endlessly
- Continue other people's conversations
- @ the bot or say "at ai" while replying to ANY message to include it in the conversation

Additionally:
- When DMing the bot, conversations continue automatically (no reply required). To start a fresh conversation, just @ the bot or say "at ai". You can still reply to continue from anywhere.
- You can branch conversations into [threads](https://support.discord.com/hc/en-us/articles/4403205878423-Threads-FAQ). Just create a thread from any message and @ the bot or say "at ai" inside to continue.
- Back-to-back messages from the same user are automatically chained together. Just reply to the latest one and the bot will see all of them.

---

### Intelligent Web Search:
The bot uses AI to automatically determine when web search would be helpful for your query. The decision is made by whatever model you're currently using. No special keywords needed!

**This feature is powered by the [RAG-Forge API](https://github.com/anojndr/RAG-Forge), which must be running separately.**

**Automatic Detection:**
- The bot analyzes every message to decide if current information would improve the response.
- Web search is enabled by default for factual questions, current events, product info, tutorials, etc.
- Simple math, creative writing, and basic definitions don't trigger web search.

**Manual Control:**
- Add `SEARCH THE NET` to your query to force web search.

**What gets searched:**
- YouTube videos (with transcripts and structured comments)
- Reddit posts and comments (with proper filtering and nested replies)
- PDF documents (full text extraction)
- Web pages (clean content extraction)

The bot intelligently generates multiple search queries when needed and combines results before responding.

---

### Google Lens Integration:
Use visual search to identify objects, landmarks, text, and more from images!

**Usage:**
- Type `googlelens <image_url>` to search for visual matches.
- Add an optional query: `googlelens <image_url> what is this building?`
- Get information about objects, landmarks, products, text in images, and similar images.

**Features:**
- **Visual Matches**: Find similar images and identify objects.
- **Related Content**: Get relevant search suggestions.
- **Text Recognition**: Extract and search text from images.
- **Smart Results**: Formatted results with sources and links.

---

### Model switching with `/model`:
![image](https://github.com/user-attachments/assets/9fbb9f56-9004-4997-a864-5b2ec67bac8f)

DiscordAIChatbot supports remote models from:
- [Google Gemini API](https://ai.google.dev/docs) (Native Provider)
- [OpenAI API](https://platform.openai.com/docs/models)
- [xAI API](https://docs.x.ai/docs/models)
- [Mistral API](https://docs.mistral.ai/getting-started/models/models_overview)
- [Groq API](https://console.groq.com/docs/models)
- [OpenRouter API](https://openrouter.ai/models)

Or run local models with:
- [Ollama](https://ollama.com)
- [LM Studio](https://lmstudio.ai)
- [vLLM](https://github.com/vllm-project/vllm)

...Or use any other OpenAI compatible API server.

---

### Gemini Native Integration & Image Generation
The bot includes a native provider for Google's Gemini models, enabling access to their unique features without an OpenAI-compatible proxy.

**Features:**
- **Direct API Access**: Connects directly to the Google AI Generative Language API.
- **Image Generation**: Supports image generation with models like `gemini-2.0-flash-preview-image-generation`.
- **Gemini-Specific Parameters**: Configure parameters like `thinking_budget` in your `config.yaml` for fine-tuned control.
- **Full Multimodality**: Seamlessly handles text and images in both prompts and responses.

---

### Persistent Message Caching
To maximize performance and minimize redundant processing, the bot caches processed message data in the PostgreSQL database.

**Features:**
- **Efficient Reprocessing**: Once a message's attachments and URLs are processed, the results are cached.
- **Faster Responses**: Subsequent replies in a conversation are faster as the bot doesn't need to re-download and re-process files.
- **Reduced API Calls**: Minimizes calls to external services for URL extraction and file processing.
- **Stateful Across Restarts**: The cache is persistent, so the bot remembers processed messages even after being restarted.

---

### Database Configuration:
The bot uses PostgreSQL for persistent storage of user preferences, API key status, message cache, and chart library data.

**Requirements:**
- PostgreSQL database server
- `database_url` configured in your `config.yaml` file

**Database URL Format:**
```yaml
database_url: postgres://username:password@localhost:5432/database_name
```

**Examples:**
- Local development: `database_url: postgres://postgres:password@localhost:5432/discordbot`
- Cloud providers: `database_url: postgresql://username:password@host:5432/database_name?sslmode=require`

Update the `database_url` field in your `configs/config.yaml` with your database credentials.

---

### Personal System Prompts with `/systemprompt`:
Each user can set their own custom system prompt that will be used instead of the default one!

**Commands:**
- `/systemprompt view` - View your current custom system prompt.
- `/systemprompt set <prompt>` - Set a custom system prompt (max 8000 characters).
- `/systemprompt clear` - Remove your custom system prompt and use the default.

**Features:**
- **Per-user customization**: Each user has their own system prompt stored in PostgreSQL.
- **Persistent storage**: Your custom prompt is saved and will be used for all future conversations.
- **Fallback to default**: If you don't have a custom prompt, the bot uses the configured default.
- **Easy management**: Simple slash commands to view, set, or clear your prompt.

---

### Response Management Tools:
Every bot response includes helpful action buttons for better interaction:

**📄 Download as Text File:**
- Download any response as a .txt file for easy saving and sharing.
- Preserves formatting and full content.

**🔗 View Output Better:**
- Creates a shareable link to text.is with improved formatting for long responses and code.

---

### Smart Table Rendering:
When the bot generates markdown tables, they're automatically converted to images for better readability on Discord.

**Features:**
- **Automatic Detection**: Detects markdown tables in responses.
- **Native Go Rendering**: Converts tables to styled PNG images using a native Go graphics library (no external browser dependencies required).
- **Clear Formatting**: Professional styling with proper borders, headers, and alternating row colors.
- **Separate Attachments**: Table images are sent as separate attachments.

---

### Dynamic Chart Generation:
The bot can execute Python code blocks to generate and display charts.

**Features:**
- **Code Execution**: Automatically detects and runs Python code blocks using `matplotlib`, `seaborn`, `plotly`, and more.
- **Image Output**: Renders the generated chart as a PNG image and sends it as an attachment.
- **Persistent Virtual Environment**: Creates and maintains a dedicated Python virtual environment (`chart_venv`) to isolate dependencies.
- **Automatic Dependency Management**: If the code requires a library that isn't installed, the bot will attempt to `pip install` it. Installed libraries are tracked in the database to avoid redundant installations.

**Example Usage:**
```
@bot please plot this data
```python
import matplotlib.pyplot as plt
import numpy as np

x = np.linspace(0, 10, 100)
y = np.sin(x)

plt.plot(x, y)
plt.title('Sine Wave')
plt.xlabel('x')
plt.ylabel('sin(x)')
plt.grid(True)
# The bot will automatically save and display this plot
```

---

### Advanced API Key Management:
Robust API key rotation and error handling for maximum uptime:

**Multiple API Keys:**
- Configure multiple API keys per provider for redundancy.
- Automatic rotation between available keys.

**Smart Error Handling:**
- Automatically detects API key failures (invalid, expired, quota exceeded).
- Marks bad keys in the database and rotates to working alternatives.
- Resets bad key status when all keys fail, giving them another chance.

**Admin Controls with `/apikeys`:**
- `/apikeys status` - View API key health across all providers.
- `/apikeys reset <provider>` - Reset bad key status for a specific provider.

**Supported for all providers:**
- OpenAI, Gemini, xAI, Mistral, Groq, OpenRouter APIs
- SerpAPI (for Google Lens)

---

### And more:
- Supports image attachments when using a vision model (like gpt-4.1, gpt-5, gpt-5-mini, claude-3, gemini-2.5-pro, etc.)
- **Enhanced text file attachments** (.txt, .c, .go, etc.) with automatic character encoding detection
- **PDF file attachments** with full text extraction support
- **Reply-based file access** - When you reply to a message with attachments, the bot automatically processes those files for context
- **Supports 50+ file formats** including source code, configuration files, documentation, and more
- User identity aware (OpenAI and xAI APIs only)
- Streamed responses (turns green when complete, automatically splits into separate messages when too long)
- Hot reloading config (you can change settings without restarting the bot)
- Displays helpful warnings when appropriate
- Caches message data in a size-managed and mutex-protected global dictionary to maximize efficiency and minimize Discord API calls
- Fully asynchronous
- High performance Go implementation with low memory footprint

## File Processing Capabilities

🚀 **Enhanced File Support:** The bot now intelligently processes a wide variety of file types with automatic format detection and encoding handling.

📄 **PDF Files:**
- Extracts plain text from PDF documents
- Handles multi-page documents

📝 **Text Files with Smart Encoding Detection:**
- Auto-detects character encoding (UTF-8, UTF-16, Latin-1, etc.)
- Supports international text (Chinese, Japanese, Korean, Russian, etc.)
- Handles legacy encodings

✅ **Supported File Formats:**
- **PDF documents** (.pdf)
- **Plain text** (.txt, .md, .log, .rst, .text)
- **Source code** (.go, .py, .js, .ts, .java, .c, .cpp, .rs, .kt, .scala, .rb, .php, .swift, .dart, .lua, etc.)
- **Configuration files** (.json, .yaml, .yml, .xml, .ini, .toml, .cfg, .conf)
- **Web files** (.html, .htm, .css, .scss, .sass, .less)
- **Data files** (.csv, .tsv)
- **And many more text-based formats**

🔗 **Reply-Based File Processing:**
- **Smart Context Inclusion**: When you reply to a message with attachments, the bot automatically processes those files for context.
- **Combined Processing**: Both original message attachments and new reply attachments are processed together.

## Instructions

1. Clone the repo:
   ```bash
   git clone https://github.com/anojndr/Discord_AI_chatbot
   ```

2. Create a copy of "config-example.yaml" named "config.yaml" and set it up:

### Configuration Settings:

| Setting | Description |
| --- | --- |
| **bot_token** | Create a new Discord bot at [discord.com/developers/applications](https://discord.com/developers/applications) and generate a token under the "Bot" tab. Also enable "MESSAGE CONTENT INTENT". |
| **client_id** | Found under the "OAuth2" tab of the Discord bot you just made. |
| **status_message** | Set a custom message that displays on the bot's Discord profile. (Max 128 characters) |
| **max_images** | The maximum number of image attachments allowed in a single message. (Default: `5`) |
| **max_messages** | The maximum number of messages allowed in a reply chain. (Default: `25`) |
| **use_plain_responses** | Set to `true` for plaintext responses instead of embeds. Disables streaming. (Default: `false`) |
| **allow_dms** | Set to `false` to disable direct message access. (Default: `true`) |
| **database_url** | **Required**. PostgreSQL connection string. Format: `postgres://user:pass@host:port/db_name` |
| **logging** | Configure logging levels. |
| **web_search** | Configure intelligent web search. **Requires the [RAG-Forge API](https://github.com/anojndr/RAG-Forge) to be running separately.** |
| **serpapi** | Configure SerpAPI for Google Lens. Supports single or multiple `api_keys`. |
| **permissions** | Configure access for `users`, `roles`, and `channels`. `admin_ids` gives users special privileges. Leave `allowed_ids` empty to allow all in a category. |
| **providers** | Add LLM providers with a `base_url` and one or more `api_keys` for rotation. |
| **models** | Define models in `<provider>/<model>` format. The first model is the default. |
| **system_prompt** | The default system prompt. Users can override with `/systemprompt`. Supports `{date}` and `{time}` tags. |
| **table_rendering** | Configure how markdown tables are rendered: `gg` (native Go, fast) or `rod` (browser, prettier). |

### API Key Rotation Setup:

For maximum reliability, configure multiple API keys per provider:

```yaml
providers:
  openai:
    base_url: https://api.openai.com/v1
    api_keys:
      - sk-your-first-key-here
      - sk-your-second-key-here
  gemini:
    api_keys:
      - your-gemini-key-1
      - your-gemini-key-2

serpapi:
  api_keys:
    - your-serpapi-key-1
    - your-serpapi-key-2
```

3. Run the bot:

   **Method 1 - Direct execution:**

   *Prerequisites:*
   - Go (version 1.23 or later)
   - Python (version 3.x, for the chart generation feature)
   - A C compiler (like `gcc` on Linux/macOS or MinGW on Windows)

   ```bash
   # Build the bot from the project root
   go build -o DiscordAIChatbot ./cmd/bot
   
   # Run the bot
   ./DiscordAIChatbot
   ```
   
   To test your LLM provider configuration, use the `--test-connectivity` flag:
   ```bash
   # Test all configured providers
   ./DiscordAIChatbot --test-connectivity all
   
   # Test a specific provider
   ./DiscordAIChatbot --test-connectivity openai
   ```

   **Method 2 - With Docker:**
   ```bash
   go run cmd/bot/main.go
   ```

## Admin Commands

Administrators (users in `permissions.users.admin_ids`) have access to:

### `/apikeys` - API Key Management
- `/apikeys status` - View the health status of all configured API keys.
- `/apikeys reset <provider>` - Reset bad key status for a provider (e.g., `openai`, `gemini`, `serpapi`).

## Notes

- **Charting Feature**: Requires a Python 3.x installation on the host. The bot creates and manages its own isolated Python virtual environment.
- **Table Rendering**: Handled by a native Go library, no external browser dependencies needed.
- **User Identity**: Supported by OpenAI and xAI API providers.
- **Google Lens**: Requires a valid SerpAPI subscription and API key.
- PRs are welcome :)

## About This Version

This is a complete Go rewrite of the Python llmcord bot from https://github.com/anojndr/llmcord, providing:
- **Better Performance**: Native Go compilation for faster execution.
- **Lower Memory Usage**: Efficient memory management.
- **Enhanced Concurrency**: Go's goroutines for better handling of multiple conversations.
- **Native Gemini Support**: Direct integration with Gemini models, including image generation.
- **Persistent Message Caching**: Caches processed message data in PostgreSQL to avoid redundant work.
- **Advanced API Management**: Multiple API key support with automatic rotation and database tracking.
- **Smart Table & Chart Rendering**: Converts markdown tables and Python chart code to images.
- **Modern Architecture**: Clean, modular codebase with proper separation of concerns.

Python version: https://github.com/anojndr/llmcord (originally based on https://github.com/jakobdylanc/llmcord)
