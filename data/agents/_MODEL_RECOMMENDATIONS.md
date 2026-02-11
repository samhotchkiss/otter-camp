# Model Recommendation Framework

## Provider Lineups (as of Feb 2026)

### Anthropic
- **Claude Opus 4.6** — Flagship. Best reasoning, longest context (200K), most capable for complex multi-step work. Expensive.
- **Claude Sonnet 5** — Best balance of capability and cost. Strong reasoning, fast. The default workhorse.
- **Claude Sonnet 4** — Previous gen, still solid. Good for simpler tasks at lower cost.
- **Claude Haiku 4** — Fast, cheap, good for simple/high-volume tasks. Limited reasoning depth.

### OpenAI
- **GPT-5.3** — Flagship. Strong reasoning, large context. Comparable to Opus.
- **GPT-5.2** — Previous flagship, still very capable.
- **o3** — Reasoning-optimized. Excellent for math, logic, code, structured analysis. Slower.
- **o4-mini** — Lighter reasoning model. Good balance for analytical tasks.
- **GPT-4o** — Multimodal, fast, affordable. Good general-purpose.

### Google
- **Gemini 2.5 Pro** — Flagship. Strong reasoning, 1M context window, good at code and analysis.
- **Gemini 2.5 Flash** — Fast, cheap, good for high-volume tasks. Thinking mode available.
- **Gemini 2.0 Flash** — Previous gen, still good for simple tasks.

### Kimi (Moonshot)
- **Kimi K2.5** — Strong coding and reasoning. 128K context. Good price/performance.
- **Kimi K2** — Previous gen, solid for general tasks.

### Ollama (Local)
- **Llama 4 Maverick** — Best open-source all-rounder. 128K context. Needs ~48GB RAM.
- **Llama 4 Scout** — Lighter Llama 4. Good for general tasks. ~24GB RAM.
- **Qwen 3 235B** — Strong reasoning and code. Needs serious hardware.
- **Qwen 3 32B** — Best mid-range local model. Fits on 32GB RAM.
- **DeepSeek R1** — Excellent reasoning. Good for code and analysis.
- **Mistral Large** — Good European alternative. Strong multilingual.
- **Phi-4** — Microsoft's small model. Good for simple tasks on minimal hardware.

## Capability Tiers

### Tier 1: Heavy Reasoning (needs flagship models)
Roles requiring: complex multi-step reasoning, system design, architecture decisions, advanced code generation, financial modeling, legal analysis, security auditing.
- **Anthropic**: Opus 4.6 (thinking: high)
- **OpenAI**: o3 or GPT-5.3 (reasoning: high) 
- **Google**: Gemini 2.5 Pro (thinking: enabled)
- **Kimi**: K2.5
- **Ollama**: Qwen 3 235B or DeepSeek R1

### Tier 2: Strong General (needs capable mid-range models)
Roles requiring: good writing, moderate reasoning, domain knowledge, consistent output quality, some code.
- **Anthropic**: Sonnet 5 (thinking: medium)
- **OpenAI**: GPT-5.3 or o4-mini
- **Google**: Gemini 2.5 Pro or Flash (thinking: enabled)
- **Kimi**: K2.5
- **Ollama**: Llama 4 Maverick or Qwen 3 32B

### Tier 3: Creative/Writing (needs strong language, less reasoning)
Roles requiring: excellent prose, voice matching, tone adaptation, creative output. Reasoning less critical.
- **Anthropic**: Sonnet 5 (thinking: low or off)
- **OpenAI**: GPT-5.3 (reasoning: off)
- **Google**: Gemini 2.5 Pro
- **Kimi**: K2.5
- **Ollama**: Llama 4 Maverick or Mistral Large

### Tier 4: Structured/Routine (can use lighter models)
Roles requiring: following templates, tracking data, scheduling, simple Q&A, categorization. Low reasoning needs.
- **Anthropic**: Haiku 4 or Sonnet 4
- **OpenAI**: GPT-4o or o4-mini
- **Google**: Gemini 2.5 Flash
- **Kimi**: K2
- **Ollama**: Qwen 3 32B or Llama 4 Scout

### Tier 5: Multilingual/Specialized
Roles requiring specific capabilities beyond general intelligence.
- Localization: Mistral Large (Ollama) or Gemini 2.5 Pro (best multilingual)
- Vision/multimodal: GPT-5.3, Gemini 2.5 Pro, or Llama 4 Maverick
