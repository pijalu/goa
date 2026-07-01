<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Wikipedia Search API Demo

This demo showcases the REST API client tool in the skill runner by performing Wikipedia searches.

## Prerequisites

- A running LLM endpoint at `http://localhost:1234/v1/chat/completions` (e.g., LM Studio, Ollama, llama.cpp server)
- Go 1.22+

## Structure

```
demo/api/
├── main.go              # Main demo program
└── skills/
    └── wiki-search/
        └── SKILL.md    # Wikipedia search skill
```

## Run

```bash
go run demo/api/main.go
```

## What it does

1. Loads the `wiki-search` skill from `./skills/wiki-search/`
2. Creates an agent with the skill runner
3. Asks the agent to search Wikipedia for "chuck norris"
4. The sub-agent uses the `rest_api` tool to call the Wikipedia API
5. Returns a list of Wikipedia article URLs

## The Skill

The `wiki-search` skill instructs the sub-agent to:
1. Use the `rest_api` tool to call the Wikipedia API
2. Parse the JSON response to extract article titles
3. Format the output as a list of Wikipedia URLs

### Wikipedia API Request

```json
{
  "method": "GET",
  "url": "https://en.wikipedia.org/w/api.php",
  "query_params": {
    "action": "query",
    "list": "search",
    "srsearch": "<search_query>",
    "format": "json",
    "utf8": "1",
    "srlimit": "10"
  }
}
```

### Expected Output

```
https://en.wikipedia.org/wiki/Chuck_Norris
https://en.wikipedia.org/wiki/Chuck_Norris_filmography
https://en.wikipedia.org/wiki/Chuck_Norris_facts
https://en.wikipedia.org/wiki/Aaron_Norris
https://en.wikipedia.org/wiki/Mike_Norris_(actor)
https://en.wikipedia.org/wiki/Walker,_Texas_Ranger
https://en.wikipedia.org/wiki/Eric_Norris
https://en.wikipedia.org/wiki/Zombie_Plane
https://en.wikipedia.org/wiki/2026_in_animation
https://en.wikipedia.org/wiki/2026_in_film
```
