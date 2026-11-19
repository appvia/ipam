# Source of vendored files

Files under `references/vendor/` are copied unchanged from GitHub Spec Kit.

- Upstream: https://github.com/github/spec-kit
- Release: v1.3.2
- Commit SHA: 55264c2f2f2d1146f02abc543cb913dcdf1f9342 (tag object bd4bd60aa94b1b867afba76c36e311990d9ea591)
- Licence: MIT
- How they were made:
  `uvx --from git+https://github.com/github/spec-kit.git@v1.3.2 specify init demo --integration claude --script sh --ignore-agent-tools --non-interactive`,
  then `.claude/skills/speckit-<step>/SKILL.md` was copied to `references/vendor/speckit-<step>.md` for specify, clarify, plan, tasks and analyze.

The files are renamed from `SKILL.md` so that they aren't installed as separate skills. To update, re-run the command at a new tag, copy again, and update this file.
