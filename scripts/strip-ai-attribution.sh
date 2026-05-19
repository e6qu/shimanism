#!/usr/bin/env bash
# Strips AI attribution trailers and trailing whitespace from commit messages.
#
# Wired in as a commit-msg pre-commit hook (.pre-commit-config.yaml). The
# user's commit policy keeps attribution noise out of the git history.

set -euo pipefail

# Portable sed -i (macOS vs Linux)
sedi() { if [[ "$OSTYPE" == darwin* ]]; then sed -i '' "$@"; else sed -i "$@"; fi; }

msg_file="$1"

# Strip trailing whitespace from every line
sedi 's/[[:space:]]*$//' "$msg_file"

# Remove Co-authored-by / Authored-by / Generated-by lines referencing AI tools
sedi -E '/^[Cc]o-[Aa]uthored-[Bb]y:.*(Claude|Copilot|GPT|Anthropic|OpenAI|AI |[Aa]rtificial|[Aa]ssistant|[Bb]ot)/d' "$msg_file"
sedi -E '/^[Aa]uthored-[Bb]y:.*(Claude|Copilot|GPT|Anthropic|OpenAI|AI |[Aa]rtificial|[Aa]ssistant|[Bb]ot)/d' "$msg_file"
sedi -E '/^[Gg]enerated-[Bb]y:.*(Claude|Copilot|GPT|Anthropic|OpenAI|AI |[Aa]rtificial|[Aa]ssistant|[Bb]ot)/d' "$msg_file"

# Remove trailing blank lines
while [ -s "$msg_file" ] && [ "$(tail -c 1 "$msg_file" | wc -l)" -eq 1 ] && [ -z "$(tail -1 "$msg_file" | tr -d '[:space:]')" ]; do
  sedi '$ d' "$msg_file"
done
