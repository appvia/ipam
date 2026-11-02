# Source of vendored files

Files under `references/vendor/` are copied unchanged from GitHub Spec Kit.

- Upstream: https://github.com/github/spec-kit
- Release: v1.2.5
- Commit SHA: 4572a67fe0e72a6837e2d7bb0fda875bc41482e5 (tag object 79b6a2ae7c7f630edd4ecd93377d0308e754e456)
- Licence: MIT
- How they were made:
  `uvx --from git+https://github.com/github/spec-kit.git@v1.2.5 specify init demo --integration claude --script sh --ignore-agent-tools`,
  then `.claude/skills/speckit-<step>/SKILL.md` was copied to `references/vendor/speckit-<step>.md` for specify, clarify, plan and tasks.

The files are renamed from `SKILL.md` so that they aren't installed as separate skills. To update, re-run the command at a new tag, copy again, and update this file.
