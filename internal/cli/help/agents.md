AGENTS AND MODELS

  yoloai ships multiple agents. Select with --agent or set a default:

     yoloai new task . --agent gemini
     yoloai config set defaults.agent gemini

AVAILABLE AGENTS

  claude (default)   Claude Code        Requires ANTHROPIC_API_KEY
  gemini             Gemini CLI         Requires GEMINI_API_KEY

  See all agents:  yoloai system agents
  Agent details:   yoloai system agents <name>

MODEL ALIASES

  Use shorthand aliases or full model names with --model:

     yoloai new task . --model sonnet     # claude-sonnet-4-latest
     yoloai new task . --model opus       # claude-opus-4-latest
     yoloai new task . --model haiku      # claude-haiku-4-latest

  Gemini aliases:

     yoloai new task . --agent gemini --model pro    # gemini-2.5-pro
     yoloai new task . --agent gemini --model flash  # gemini-2.5-flash

  Set a default model:

     yoloai config set defaults.model sonnet

LOCAL MODELS

  Aider supports local model servers (Ollama, LM Studio):

     yoloai config set defaults.env.OLLAMA_API_BASE \
       http://host.docker.internal:11434

More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#agents-and-models
