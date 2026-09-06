---
name: "AskISO"
short_name: "AskISO"
title: "AskISO news and releases"
description: "Release notes, changes that affect the messages you send, and where to follow along. Every release is published with what changed and why."
keywords: "AskISO releases, ISO 20022 tooling news, AskISO changelog, November 2026 updates"
author: "Sebastien Rousseau"
date: "2026-08-27"
news_publication_date: "2026-08-27"
layout: "page"
language: "en-GB"
schema: "page"
changefreq: "weekly"
copyright_year: "2026"
form_origin: "https://askiso.io"
nav_news: "true"
banner: "getty-images-LaU3HadwEeE-unsplash"
banner_alt: "A financial chart drawn in blue over a dark background."
eyebrow: "News"
headline: "What changed, and when"
lead: "Every release says what moved and why. Nothing here is announcement for its own sake."
---

## 27 August 2026 — Swift defers the structured address migration

Swift has accepted a community request to extend the structured address
migration, and has deferred every payments change in Standards Release 2026.
There is no new date yet, and Swift will set the timing by December at the
latest. Securities and trade changes are split off and now go live in Q1 2027.

[Read what changed, and what it means for you](/news/swift-defers-structured-address-migration/)

## Where releases are published

The canonical list is [releases on GitHub](https://github.com/sebastienrousseau/askiso/releases), where each carries notes describing what changed, binaries for every platform, a software bill of materials, and a verifiable Sigstore signature.

To follow along without visiting: subscribe to the [feed](/rss.xml) in any
reader, watch releases on GitHub, or ask on the [contact page](/contact/) to be
told by email when something changes that affects the messages you send.

## v0.0.3 — 5 September 2026

The private CBPR+ workspace. An institution that holds the Swift Usage
Guidelines for SR2025 can now build a versioned workspace from its own
export, check messages against it, and keep the evidence, without any Swift
content leaving the machine. The site was checked fact by fact against the
code and against Swift's own notices, and every page gained social
descriptions, breadcrumbs and software markup. The build is green again on
Linux, macOS and Windows, and the [GitHub Action](https://github.com/marketplace/actions/askiso-iso-20022-check)
is listed on the Marketplace.

## v0.0.2 — 28 August 2026

The website release, which introduced the commercial navigation, four hub
pages, a contact page, the vision and knowledge centre pages, a dedicated 404
page and the analysis of Swift's deferral. Every page acquired a photographic
masthead, and an accessibility gate now fails the deployment rather than
letting the WCAG 2.2 claim go stale. The tool itself stopped asserting a
cutover date the requirement no longer has.

## v0.0.1 — 26 August 2026

The first tagged release. It establishes what the project does, and the standard it holds itself accountable to.

Validation agreeing with libxml2 on the whole catalogue. Linting that names the
rule, the path and the remediation. The CBPR+ scheme profiles including the
November 2026 address mandate. Conversion between seven SWIFT MT types and
their ISO 20022 equivalents, each with a fidelity report. All 2,845 message
definitions indexed offline. Five ways to run the same engine.

Versions increment by 0.0.1 from here. Maturity is earned in public over many
small releases rather than claimed with a large first number, so v0.1.0 follows
v0.0.99 and means only that a hundred releases have shipped.

## The dates that matter more than ours

Three regulatory deadlines determine what this project prioritises, and none of them are ours.

**Structured addresses, timing to be confirmed.** CBPR+ requires structured or
hybrid postal addresses on cross-border payments, and unstructured ones are
rejected outright. Swift deferred the 14 November 2026 cutover and will confirm
new timing by December, so the [briefing](/deadline/) says what the rule
requires rather than when it starts.

**November 2027.** Exceptions and investigations move to ISO 20022. The MT
n95 and n99 enquiry messages give way to camt.110, camt.111, camt.056 and
camt.029.

**November 2028.** The remaining MT categories retire: statements (MT940,
MT942, MT950), direct debits (MT104, MT107, MT204) and charges. Swift's
deferral covers the 2026 payments changes only; the later phases stand as
published.

## Reporting something

A message AskISO handles incorrectly remains the most valuable contribution anyone can make. If
a payment was accepted here and rejected by your counterparty, or the reverse,
the [contact page](/contact/) reaches a person directly.
