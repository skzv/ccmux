# Delivery verification — 2026-09-08

- App: [PR #189](https://github.com/skzv/ccmux/pull/189), released as [v0.4.0](https://github.com/skzv/ccmux/releases/tag/v0.4.0) at `0c229da`.
- Website: [PR #18](https://github.com/skzv/ccmux-website/pull/18) plus final copy polish [PR #19](https://github.com/skzv/ccmux-website/pull/19), deployed at `365d4ee`.
- App validation: full Go suite, race tests, format/vet, macOS and Linux integration, fuzz smoke tests and all six cross-compilation targets passed.
- Website validation: Astro check/build, eight static checks, and all 72 desktop/mobile browser tests passed locally and against https://ccmux.ai/.
- Production audit: all 162 pages matched local titles, descriptions, canonical/hreflang links, locale tags, structured data and headings. The sitemap contains 153 indexable URLs. All nine missing-page routes return localized HTTP 404 responses with noindex; www redirects to the canonical host.
- nginx: installed all eight translated-prefix locations, added an explicit UTF-8 response charset, validated configuration and reloaded. Previous site build and nginx configuration were backed up before deployment.
- Release: verified SHA-256 checksums and all three binaries in each of four platform archives; ran version and language-list checks on macOS arm64 and Linux amd64.
- Homebrew: release publication succeeded, but automatic tap update returned HTTP 401 from the existing invalid `HOMEBREW_TAP_GITHUB_TOKEN`. Updated the formula to the verified v0.4.0 URLs/checksums in tap commit `93c1943`; the public-tap macOS installation and binary smoke test [passed](https://github.com/skzv/homebrew-tap/actions/runs/34266206488). Renewing that narrowly scoped secret remains a maintenance task for future automated releases.

New catalogs use translation assistance with reviewed primary UI and SEO wording. Native-language contributions are invited in both products and their contribution guides.
