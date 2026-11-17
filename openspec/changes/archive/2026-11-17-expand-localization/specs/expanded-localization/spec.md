## ADDED Requirements

### Requirement: Matching app and website languages
The app and website SHALL support en, zh, es, ja, ko, fr, de, pt-br, and ru with complete UI and page catalogs. Commands, code and placeholders MUST remain intact.

#### Scenario: Switch an app language
- **WHEN** a user selects any supported language through settings or the CLI
- **THEN** the interface renders that language and retains valid formatted messages

#### Scenario: Resolve a regional environment locale
- **WHEN** the configured language is empty and LC_ALL or LANG names a supported regional locale
- **THEN** the corresponding supported language is selected with LC_ALL taking precedence

### Requirement: Crawlable localized website
Each localized page SHALL have translated prose, title and description, its own canonical, reciprocal alternates for every supported language and x-default, localized structured data, and a sitemap entry unless noindex.

#### Scenario: Open documentation without JavaScript
- **WHEN** a reader opens a localized documentation URL with JavaScript disabled
- **THEN** the prose is translated, examples remain unchanged, and a usable language selector links to counterpart pages

#### Scenario: Missing localized page
- **WHEN** a reader requests a nonexistent URL under a supported locale
- **THEN** the site returns HTTP 404 with a localized noindex error page
