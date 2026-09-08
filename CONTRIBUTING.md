# Contributing to ccmux

Bug fixes, clearer docs, translations, and small usability improvements are welcome.

- [Report an issue](https://github.com/skzv/ccmux/issues/new) with your ccmux version, operating system, steps to reproduce, and expected behavior. Remove secrets from logs and configuration snippets.
- [Submit a pull request](https://github.com/skzv/ccmux/compare). Describe the problem, resulting behavior, and relevant checks. For a substantial feature, open an issue first to discuss scope.
- [Improve website copy or documentation translations](https://github.com/skzv/ccmux-website/blob/main/CONTRIBUTING.md) in the website repository.

## TUI translations

Edit `internal/i18n/locales/<code>.toml`. English phrases are keys; translations are values. Supported languages: English, Simplified Chinese, Spanish, Japanese, Korean, French, German, Brazilian Portuguese, and Russian.

Keep printf placeholders (`%s`, `%d`, etc.) and their argument types intact. If grammar requires reordering, use indexed Go verbs such as `%[2]s` and `%[1]d`, and check the rendered message. Preserve paths, command names, flags, keyboard shortcuts, and protocol identifiers. Use concise labels that fit small terminals. New catalogs use translation assistance; corrections from native speakers are welcome.

Select a language with `ccmux language <code>` and reopen the app, or change it immediately in Settings. Preview the relevant screens at phone and desktop widths.

## Development checks

Read `CLAUDE.md` for architecture and repository conventions. Install Go 1.26 or newer, then run:

```sh
go test ./internal/i18n ./internal/tui ./cmd/ccmux/cmd
go test ./...
make lint
make build
```

Every new behavior needs a meaningful test. Localization tests check catalog coverage and formatting placeholders. Keep pull requests focused, and explain how a reviewer can verify the change.
