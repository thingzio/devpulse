# Contributing to devpulse

Thank you for your interest in contributing to devpulse! We welcome contributions from developers of all backgrounds and experience levels.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Project governance](#project-governance)
- [Developer Certificate of Origin](#developer-certificate-of-origin)
- [Getting Started](#getting-started)
- [How to Contribute](#how-to-contribute)
- [Design Principles](#design-principles)
- [Pull Request Process](#pull-request-process)
- [Tips for Contributors](#tips-for-contributors)

## Code of Conduct

This project follows a commitment to fostering an open and welcoming environment. Please be respectful and professional in all interactions. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for details.

## Project governance

This is a single-maintainer project. Decisions — what gets merged, what ships, what is in scope — are the maintainer's, and the maintainer is listed in [MAINTAINERS.md](MAINTAINERS.md). There is no steering committee and no vote, because there are not enough people for either to mean anything.

**There is no review-time commitment.** A pull request may be reviewed the same day or may sit for weeks. The project is maintained on a best-effort basis, and pretending otherwise would set an expectation that gets broken. If something is urgent for you, say so in the pull request — it helps with ordering, though it is not a guarantee.

What this means in practice:

- **Small, focused pull requests get reviewed fastest.** A large change touching many packages is not refused, but it will wait longer.
- **Open an issue before a large change.** Finding out that a direction is wrong after a week of work is worse for you than for the project.
- **A closed pull request is not a judgment on you.** Scope is the most common reason; this is a reference implementation, not a product trying to satisfy everyone.

Adding a second maintainer is described in [MAINTAINERS.md](MAINTAINERS.md). Short version: sustained, substantive contribution plus a willingness to take the responsibility.

## Developer Certificate of Origin

This project requires the [Developer Certificate of Origin](https://developercertificate.org) (DCO). It is not a CLA — you keep your copyright, and you are not assigning anything. You are certifying that you wrote the contribution or otherwise have the right to submit it under the project's license.

Certify it by adding a `Signed-off-by` trailer to every commit:

```shell
git commit -s -m "feat: add network stats"
```

which appends:

```
Signed-off-by: Your Name <your.email@example.com>
```

The name and email must be real and must match your Git configuration. The DCO bot checks every commit in a pull request and will fail the check if any commit is missing the trailer.

**`-s` and `-S` are different flags and this project wants both.** `-s` adds the sign-off trailer described above. `-S` cryptographically signs the commit with your key. Together:

```shell
git commit -S -s -m "feat: add network stats"
```

**If you forgot to sign off**, rewrite the commits on your branch and force-push:

```shell
git rebase --signoff main
git push --force-with-lease
```

## Getting Started

Before contributing:

1. Read the [README.md](README.md) to understand the project
2. Check existing [issues](https://github.com/thingzio/devpulse/issues) to avoid duplicates
3. Set up your development environment following [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)

## How to Contribute

### Reporting Bugs

> **Security vulnerabilities do not go here.** Report them privately through [GitHub Security Advisories](https://github.com/thingzio/devpulse/security/advisories/new). See [SECURITY.md](SECURITY.md) for scope and expected response times.

- Use [GitHub Issues](https://github.com/thingzio/devpulse/issues/new) to report bugs
- Describe the issue clearly with steps to reproduce
- Include system information (OS, Go version)
- Attach logs or screenshots if applicable
- Check if the issue already exists before creating a new one

### Suggesting Enhancements

- Open a [GitHub Issue](https://github.com/thingzio/devpulse/issues/new) describing the feature
- Clearly describe the proposed feature and its use case
- Explain how it benefits the project and users

### Improving Documentation

- Fix typos, clarify instructions, or add examples
- Update README.md for user-facing changes
- Ensure code comments are accurate and helpful

### Contributing Code

- Fix bugs, add features, or improve performance
- Follow the development workflow in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)
- Ensure all tests pass and code meets quality standards
- Write tests for new functionality

#### Go dependencies (vendor)

This project vendors Go dependencies. After changing `go.mod` or `go.sum`, run `make tidy` (which runs `go mod vendor`) and commit `go.mod`, `go.sum`, and the `vendor/` directory. CI will fail if `vendor/` is out of sync.

## Design Principles

These principles guide all design decisions in devpulse. When faced with trade-offs, these principles take precedence.

### Correctness Must Be Reproducible

Given the same inputs, the same version must always produce the same result.

**What:** No hidden state, no implicit defaults, no non-deterministic behavior.

**Why:** Reproducibility is a prerequisite for debugging, validation, and trust.

### Partial Failure Is the Steady State

Design for partitions, timeouts, and bounded retries.

**What:** GitHub API calls may fail, rate limits may be hit, network may be unreliable. Every external interaction must handle failure gracefully.

**Why:** The GitHub API is the primary external dependency. Users import large datasets that take many API calls. Resilience is not optional.

### Boring First

Default to proven, simple technologies.

**What:** PostgreSQL for storage, `net/http` for the server, `log/slog` for logging. No frameworks, no ORMs, no unnecessary abstractions.

**Why:** Simplicity reduces bugs, makes debugging easier, and keeps the project accessible to new contributors.

### Trust Requires Verifiable Provenance

Every released artifact carries verifiable proof of origin and build process.

**What:** Container images are built in CI from pinned dependencies and pushed to Artifact Registry. Vulnerability scanning runs on every push.

**Why:** "Trust us" is not a security model.

## Pull Request Process

### Before Submitting

1. **Ensure all checks pass:**
   ```bash
   make qualify
   ```

2. **Update documentation if needed:**
   - README.md for user-facing changes
   - docs/DEVELOPMENT.md for developer workflow changes

3. **Sign off your commits** (required — see [Developer Certificate of Origin](#developer-certificate-of-origin)):
   ```bash
   git commit -S -s -m "feat: add network stats"
   ```

### Creating the Pull Request

1. Push your branch and open a PR against `main`
2. Provide a clear summary of changes
3. Reference related issues (e.g., "Fixes #123")

### Review Process

1. **Automated checks** run via GitHub Actions:
   - Go tests with race detector
   - golangci-lint
   - Vulnerability scan (govulncheck)

2. **Maintainer review** covers:
   - Correctness and test coverage
   - Code style and Go idioms
   - Consistency with existing patterns

3. **Address feedback** by pushing new commits

4. **Merge**: Once approved and CI passes, a maintainer will merge

### After Merging

```bash
git checkout main
git pull origin main
git branch -d your-branch
```

## Tips for Contributors

### First-Time Contributors

**Recommended starting points:**

1. Start with issues labeled `good first issue`
2. Read existing code in the package you're modifying before writing
3. Study the [Design Principles](#design-principles) section

**Good first contributions:**

- Documentation improvements (typos, clarifications)
- Adding test cases to existing tests
- Improving error messages with better context

### Code Style

- Follow existing patterns in the codebase
- Use `fmt.Errorf("context: %w", err)` for error wrapping
- Use `log/slog` for all logging (never `fmt.Println`)
- Write table-driven tests for multiple test cases
- Sentinel errors as package-level vars (e.g., `errDBNotInitialized`)

### Writing Good Commit Messages

```
Short summary (50 chars or less)

More detailed explanation if needed. Wrap at 72 characters.
Explain the problem being solved and why this approach was chosen.

- Use present tense ("Add feature" not "Added feature")
- Reference issues: "Fixes #123" or "Related to #456"
```

### Getting Help

- **GitHub Issues**: [Create an issue](https://github.com/thingzio/devpulse/issues/new) with the "question" label
- **Existing Issues**: Search for similar questions first

## Additional Resources

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) - Development setup, architecture, and tooling
- [README.md](README.md) - Project overview and quick start

Thank you for contributing to devpulse!
