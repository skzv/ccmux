## Context

Both products use English phrases as translation keys. Go embeds TOML and currently stores a Boolean language flag; Astro statically translates rendered HTML through a Chinese JSON catalog. The website has 18 logical pages per locale.

## Goals / Non-Goals

**Goals:** Nine matching languages; deterministic locale resolution; full catalog coverage; keyboard-accessible language selection; localized search metadata; contribution links.

**Non-Goals:** Translate user content, code samples, agent output or daemon wire values; add runtime translation calls; force locale redirects based on browser language.

## Decisions

- Store an atomic language identifier and immutable embedded catalogs in Go. Parse exact language tags with regional aliases instead of broad prefix matching; keep English fallback and LC_ALL precedence.
- Keep English at existing routes and Chinese at /zh/. Use /es/, /ja/, /ko/, /fr/, /de/, /pt-br/, /ru/ for new pages. A shared website locale registry drives routes, menus, language tags, formatting and sitemap configuration.
- Use a native details/summary language menu with real links. It fits narrow screens and works without JavaScript; enhancement preserves query and fragment when switching.
- Translate all static prose and metadata during build. Preserve code, URLs, command flags, format placeholders and protocol identifiers; verify catalogs and rendered examples.
- Put contribution links in existing help/docs surfaces plus a CLI contribution command. No interrupting popups or repeated nagging.

## Risks / Trade-offs

- Longer translations can overflow terminals or navigation → test narrow layouts and use compact UI translations.
- Assisted translations can lose technical meaning → review key UI/SEO strings, validate placeholders, and invite native-language corrections.
- Locale lists can drift across products → document exact matching codes and assert complete catalogs.

## Migration Plan

Merge verified app and website PRs, publish the app release when appropriate, deploy the staged website, then verify all localized pages and errors. Preserve the previous site build for rollback.

## Open Questions

None; Russian joins the six suggested languages requested by the user.
