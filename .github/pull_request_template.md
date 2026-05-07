## Governance Evidence

Team/reviewer IDs or links:

Worker/subagent ownership:

Main agent did not directly write code/docs provenance:

TDD red/green evidence:

Precise test commands:

`scripts/verify-ga-release.sh` result:

Risk/gate impact:

Product-agnostic boundary check:

Package/module naming review:

## Reviewer Checklist

- [ ] Team/reviewer IDs or links are present and actionable.
- [ ] Worker/subagent ownership is clear for code and docs changes.
- [ ] TDD red/green evidence is included for behavioral or governance guards.
- [ ] Precise test commands and outcomes are listed.
- [ ] `scripts/verify-ga-release.sh` result is recorded or explicitly marked not run with reason.
- [ ] Main agent did not directly write code/docs provenance is recorded.
- [ ] Risk/gate impact is stated without closing any gate unless separately accepted.
- [ ] Product-agnostic boundary check confirms no caller-specific package names, fixture names, or docs wording were introduced.
- [ ] Package/module naming review confirms any module path mention is intentional repo identity, not caller-specific vocabulary.
