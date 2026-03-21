# Git Commit Convention

Use this format for commit subjects:

```text
type(scope): short summary
```

Recommended `type` values:

- `feat`: new user-facing capability
- `fix`: bug fix or regression fix
- `docs`: documentation-only change
- `refactor`: code cleanup without behavior change
- `test`: tests only
- `chore`: tooling, config, scripts, or maintenance

Examples:

- `feat(runtime): assess uploaded h5ad readiness`
- `fix(ui): show runtime mode in system status`
- `docs(help): add subcluster usage example`

Keep the subject under about 72 characters and use imperative mood.

For the commit body, follow the local template in `.gitmessage.txt`:

- `Why`: why this change is needed
- `What`: what changed
- `Verify`: what commands or checks passed
- `Refs`: optional issue or task reference
