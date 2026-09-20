# llmsh

Publish and install versioned skills for AI agents, from
[LLM SkillHub](https://llmskillhub.com).

A skill is a folder of instructions, scripts and references that an agent
loads. `llmsh` packages one, checks it against the
[Agent Skills spec](https://agentskills.io/specification), submits it for
review, and installs published ones with their tree digest verified.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Priy6anshu/llmsh/main/install.sh | sh
```

```sh
npm install -g llmskillhub     # the command is still llmsh
```

```sh
go install github.com/Priy6anshu/llmsh/cmd/llmsh@latest
```

Or take a binary from [releases](https://github.com/Priy6anshu/llmsh/releases).
Every release publishes `SHA256SUMS`; the installer checks it and refuses to
install a file that does not match.

## Use

```sh
llmsh install anthropics/mcp-builder     # download, verify, unpack
llmsh install name@1.2.0                 # an exact version
llmsh validate ./my-skill                # check it locally, storing nothing
llmsh publish ./my-skill                 # submit it for review
llmsh status owner/skill                 # versions and review state
llmsh login                              # store an access token
```

`llmsh` reads the repository when publishing: it will not ship uncommitted work
by accident, and it records the commit a version came from.

## Configuration

| | |
|---|---|
| `LLMSH_API` | API address, default `https://api.llmskillhub.com` |
| `LLMSH_INGEST` | upload address, default the same |
| `LLMSH_TOKEN` | a token for CI, instead of the stored one |

The stored token lives in your user config directory, `0600`, and is the only
thing written there.

## What happens when you publish

Nothing is public straight away. A version is unpacked, validated against the
spec, and scanned for credentials and prompt injection; a live credential
blocks it outright, and everything else is flagged for a person who reads it
before it is listed. Published versions are immutable — the tree digest is the
version's identity, and `llmsh install` checks it before writing a file.

## This repository

Generated from the private monorepo that also holds the services. The CLI is
the part strangers are asked to run, so it is the part that has to be readable.
Issues and pull requests are welcome here; changes land upstream and are
synced back.
