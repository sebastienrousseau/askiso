// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sebastienrousseau/askiso/internal/catalog"
)

// Engine handles ISO 20022 conversational Q&A, local AI queries, and RAG.
type Engine struct {
	Index *catalog.Index
}

// New creates a new AI engine.
func New(idx *catalog.Index) *Engine {
	return &Engine{
		Index: idx,
	}
}

// MessageAnswer contains the structured reply for an AI query.
type MessageAnswer struct {
	Summary     string
	Details     string
	Suggestions []string
	RelatedMsgs []catalog.Message
	// ProviderWarning explains why an explicitly configured model provider was
	// not used. The offline answer still succeeds, but the fallback is no
	// longer silent.
	ProviderWarning string
}

var (
	msgCodeRegex   = regexp.MustCompile(`(?i)\b([a-z]{4})[\._\-\s]?([0-9]{3})(?:[\._\-\s]?([0-9]{3})[\._\-\s]?([0-9]{2}))?\b`)
	domainKeywords = map[string]string{
		"pacs": "Payments Clearing and Settlement (Interbank)",
		"pain": "Payments Initiation (Customer-to-Bank)",
		"camt": "Cash Management & Account Reporting",
		"acmt": "Account Management & Account Switching",
		"seev": "Securities Events & Corporate Actions",
		"sese": "Securities Settlement & Reconciliation",
		"setr": "Securities Trade & Investment Funds",
		"colr": "Collateral Management",
		"fxtr": "Foreign Exchange Trade",
		"redp": "Post-Trade Matching",
		"tsmt": "Trade Services Management",
		"auth": "Authorities & Regulatory Reporting",
		"catp": "ATM Interface & Financial Retail",
		"catm": "Terminal Management",
		"caaa": "Card Payments Acceptor-to-Acquirer",
		"cain": "Acquirer-to-Issuer Card Messages",
		"head": "Business Application Header (BAH)",
		"remt": "Stand-Alone Remittance Advice",
	}
)

// NormalizeQuery extracts any ISO 20022 message codes and standardizes them.
func NormalizeQuery(input string) string {
	indices := msgCodeRegex.FindAllStringSubmatchIndex(input, -1)
	if len(indices) == 0 {
		return input
	}
	var b strings.Builder
	b.Grow(len(input))
	last := 0
	for _, loc := range indices {
		b.WriteString(input[last:loc[0]])
		prefix := strings.ToLower(input[loc[2]:loc[3]])
		num := input[loc[4]:loc[5]]
		if loc[6] >= 0 && loc[8] >= 0 {
			fmt.Fprintf(&b, "%s.%s.%s.%s", prefix, num, input[loc[6]:loc[7]], input[loc[8]:loc[9]])
		} else {
			fmt.Fprintf(&b, "%s.%s", prefix, num)
		}
		last = loc[1]
	}
	b.WriteString(input[last:])
	return b.String()
}

// Query processes a natural language prompt and returns an answer.
func (e *Engine) Query(prompt string) (answer MessageAnswer) {
	var providerWarning string
	defer func() { answer.ProviderWarning = providerWarning }()
	rawPrompt := strings.TrimSpace(prompt)
	if rawPrompt == "" {
		return MessageAnswer{
			Summary: "AskISO Ask AI",
			Details: "Please enter a question, message identifier (e.g. `pacs008`, `pain001`, `camt053`), or business topic.",
			Suggestions: []string{
				"What is pacs.008?",
				"Compare pacs.008 vs pacs.009",
				"How does camt.053 work?",
				"Explain Business Application Header",
			},
		}
	}

	normPrompt := NormalizeQuery(rawPrompt)
	p := strings.ToLower(normPrompt)

	// 1. Check for external / local LLMs (OpenAI / Ollama / Gemini / Claude)
	llmResp, ok, providerWarning := e.queryExternalLLM(rawPrompt)
	if ok && len(llmResp) > 15 {
		return MessageAnswer{
			Summary: "AskISO Ask AI (Connected LLM)",
			Details: llmResp,
		}
	}

	// 2. High-Precision Comparisons
	if (strings.Contains(p, "pacs.008") || strings.Contains(p, "pacs008")) && (strings.Contains(p, "pacs.009") || strings.Contains(p, "pacs009")) {
		return MessageAnswer{
			Summary: "Comparison: pacs.008 vs pacs.009",
			Details: `**TL;DR**: **pacs.008** transfers funds for **non-bank customers** (carrying full debtor/creditor data), whereas **pacs.009** moves funds exclusively between **financial institutions** for treasury, FX, or liquidity cover.

## Comparison Overview
• **pacs.008 (Customer Transfer)**:
  - **Actors:** Customer (Debtor) ➔ Customer (Creditor)
  - **Legacy MT:** Replaces **MT103** / **MT103 STP** / **MT103 REMIT**
  - **Compliance:** FATF 16 travel rule, AML/sanctions on Dbtr/Cdtr
  - **Charges:** Supports **DEBT**, **CRED**, **SHAR** allocation
  - **Use Cases:** Commercial wires, invoices, retail payments

• **pacs.009 (FI Transfer & Cover)**:
  - **Actors:** Bank ➔ Bank (Financial Institutions)
  - **Legacy MT:** Replaces **MT202** (CORE) / **MT202 COV** (Cover)
  - **Modes:** **CORE** (FI-to-FI) & **COVE** (Cover for pacs.008)
  - **Charges:** **SLEV** (Service Level) standard
  - **Use Cases:** Treasury, FX settlement, liquidity funding

---

## Cover Payment Workflow (COVE)
When debtor and creditor banks lack a direct account relationship:
` + "```text" + `
Customer Leg (pacs.008):  Debtor Bank ────────► Creditor Bank
Cover Leg    (pacs.009):  Debtor Bank ──► RTGS ──► Creditor Bank
` + "```" + ``,
			Suggestions: []string{"Show pacs.008 XML", "Show pacs.009 XML", "Explain cover payments in pacs.009"},
			RelatedMsgs: e.Index.Search("pacs.008"),
		}
	}

	if (strings.Contains(p, "pain.001") || strings.Contains(p, "pain001")) && (strings.Contains(p, "pacs.008") || strings.Contains(p, "pacs008")) {
		return MessageAnswer{
			Summary: "Comparison: pain.001 vs pacs.008",
			Details: `**TL;DR**: **pain.001** is the **client-to-bank initiation** message (customer instructing bank), while **pacs.008** is the **bank-to-bank clearing and settlement** message.

## Comparison Overview
• **pain.001 (Customer Initiation)**:
  - **Lifecycle Leg:** INITIATION (Corporate/Client ➔ Debtor Bank)
  - **Batching:** Multi-payment batches (single debit, multi-credit)
  - **Legacy Format:** Replaces EDIFACT PAYMUL & corporate banking files
  - **Primary Actor:** Non-bank Ordering Customer

• **pacs.008 (Interbank Settlement)**:
  - **Lifecycle Leg:** CLEARING & SETTLEMENT (Debtor Bank ➔ Creditor Bank)
  - **Batching:** Individual interbank transaction blocks
  - **Legacy Format:** Replaces SWIFT MT103
  - **Primary Actor:** Financial Institutions & Clearing Rails (RTGS)`,
			Suggestions: []string{"Show pain.001 XML", "Show pacs.008 XML", "What is pain.002?"},
			RelatedMsgs: e.Index.Search("pain.001"),
		}
	}

	if (strings.Contains(p, "camt.052") || strings.Contains(p, "camt052")) && (strings.Contains(p, "camt.053") || strings.Contains(p, "camt053")) {
		return MessageAnswer{
			Summary: "Comparison: camt.052 vs camt.053",
			Details: `**TL;DR**: **camt.052** delivers **real-time intraday balance reports** with pending items, while **camt.053** provides the **official end-of-day statement** with final booked entries.

## Comparison Overview
• **camt.052 (Account Report)**:
  - **Timing:** Intraday / Interim (real-time snapshots throughout day)
  - **Finality:** Contains both booked and **pending/tentative** items
  - **Balances:** Interim Booked (**ITBD**), Interim Available (**ITAV**)
  - **Legacy MT:** Replaces **MT942** (Interim Transaction Report)

• **camt.053 (Account Statement)**:
  - **Timing:** End-of-Day or periodic accounting closing
  - **Finality:** Contains **final, irrevocably booked** transactions only
  - **Balances:** Opening Booked (**OPBD**), Closing Booked (**CLBD**)
  - **Legacy MT:** Replaces **MT940** (Customer Statement) & **MT950**`,
			Suggestions: []string{"Show camt.052 XML", "Show camt.053 XML", "What is camt.054?"},
			RelatedMsgs: e.Index.Search("camt.053"),
		}
	}

	// 2.5 XML Sample and XSD Schema Query Handler
	if strings.Contains(p, "xml") || strings.Contains(p, "sample") || strings.Contains(p, "xsd") || strings.Contains(p, "schema") {
		matches := msgCodeRegex.FindAllString(normPrompt, -1)
		if len(matches) > 0 {
			for _, m := range matches {
				normM := NormalizeQuery(m)
				results := e.Index.Search(normM)
				if len(results) > 0 {
					top := results[0]
					if strings.Contains(p, "xsd") || strings.Contains(p, "schema") {
						return MessageAnswer{
							Summary: fmt.Sprintf("XSD Schema: %s", top.ID),
							Details: fmt.Sprintf("### XSD Schema: %s\n\n• Category: %s\n• Version: %s\n• XSD File Path: `%s`\n\nTo inspect full element definitions in the viewer, run:\n```bash\n./askiso info %s\n```", top.ID, top.Category, top.Version, top.XSDPath, top.ID),
							Suggestions: []string{
								fmt.Sprintf("Show %s XML", top.ID),
								fmt.Sprintf("What is %s?", top.BaseCode),
							},
							RelatedMsgs: results,
						}
					}

					// XML Sample
					samplePreview := ""
					if top.XMLSamplePath != "" {
						if data, err := os.ReadFile(top.XMLSamplePath); err == nil {
							lines := strings.Split(string(data), "\n")
							if len(lines) > 20 {
								samplePreview = strings.Join(lines[:20], "\n") + "\n... [truncated]"
							} else {
								samplePreview = string(data)
							}
						}
					}

					detailsText := fmt.Sprintf("### XML Sample: %s\n\n• Category: %s\n• Version: %s\n• File Path: `%s`", top.ID, top.Category, top.Version, top.XMLSamplePath)
					if samplePreview != "" {
						detailsText += fmt.Sprintf("\n\n#### XML Payload Preview:\n```xml\n%s\n```", samplePreview)
					}
					return MessageAnswer{
						Summary: fmt.Sprintf("XML Sample: %s", top.ID),
						Details: detailsText,
						Suggestions: []string{
							fmt.Sprintf("Show %s XSD", top.ID),
							fmt.Sprintf("What is %s?", top.BaseCode),
						},
						RelatedMsgs: results,
					}
				}
			}
		}
	}

	// 2.6 Cover Payments Deep Dive
	if strings.Contains(p, "cover") && (strings.Contains(p, "pacs.009") || strings.Contains(p, "pacs009") || strings.Contains(p, "payment")) {
		matches := e.Index.Search("pacs.009")
		return MessageAnswer{
			Summary: "Cover Payments (pacs.009 COVE)",
			Details: `### Cover Payments Architecture (pacs.009 COVE)

A **Cover Payment** is used in international correspondent banking when the Debtor Bank and Creditor Bank do not maintain direct nostro/vostro accounts with each other.

#### Dual-Message Architecture:
1. **Direct Customer Leg (pacs.008)**:
   Sent directly from Debtor Bank to Creditor Bank (via SWIFT FIN/CBPR+) containing full ordering customer & beneficiary data (FATF 16).
2. **Reimbursement / Cover Leg (pacs.009 COVE)**:
   Sent through intermediary clearing banks (RTGS/TARGET2/Fedwire) to move the actual settlement liquidity.

#### Key Identifier & Reconciliation:
• Both pacs.008 and pacs.009 COVE MUST share the exact same **UETR** (UUIDv4) and **EndToEndId** to allow automatic straight-through reconciliation (STP) at the Creditor Bank.`,
			Suggestions: []string{
				"Show pacs.009 XML",
				"Compare pacs.008 vs pacs.009",
				"What is pacs.008?",
			},
			RelatedMsgs: matches,
		}
	}

	// 3. Specific Standard Message Handlers
	if strings.Contains(p, "pacs.008") || strings.Contains(p, "pacs008") || strings.Contains(p, "customer credit transfer") {
		matches := e.Index.Search("pacs.008")
		return MessageAnswer{
			Summary: "Financial Institutional Customer Credit Transfer (pacs.008)",
			Details: `**TL;DR**: **pacs.008** (FIToFICustomerCreditTransfer) is the core ISO 20022 interbank message used to execute customer-initiated credit transfers across RTGS, SEPA, and SWIFT CBPR+ rails.

## Core Payment Flow
` + "```text" + `
Debtor ──[pain.001]──► Debtor Bank ──[pacs.008]──► RTGS
RTGS   ──[pacs.008]──► Creditor Bank
` + "```" + `

---

## Key XML Elements
- **GrpHdr:** Group Header ('MsgId', 'CreDtTm', 'NbOfTxs')
- **SttlmInf:** Settlement Info ('CLRG', 'INDA', 'INGA', 'COVE')
- **CdtTrfTxInf:** Credit Transfer Details:
  - **PmtId/EndToEndId:** End-to-end reconciliation identifier
  - **PmtId/UETR:** Universal Unique UUIDv4 transaction reference
  - **IntrBkSttlmAmt:** Interbank settlement currency and amount
  - **Dbtr & Cdtr:** Ordering Customer & Beneficiary details & IBAN
  - **RmtInf:** Structured or Unstructured Remittance Information`,
			Suggestions: []string{"Show pacs.008 XML", "Show pacs.008 XSD", "Compare pacs.008 vs pacs.009"},
			RelatedMsgs: matches,
		}
	}

	if strings.Contains(p, "pacs.009") || strings.Contains(p, "pacs009") || strings.Contains(p, "financial institution credit transfer") {
		matches := e.Index.Search("pacs.009")
		return MessageAnswer{
			Summary: "Financial Institution Credit Transfer (pacs.009)",
			Details: `**TL;DR**: **pacs.009** (FinancialInstitutionCreditTransfer) moves funds exclusively between **financial institutions** for treasury, FX settlement, or cover liquidity.

## Core Message Modes
- **pacs.009 CORE**: Direct bank-to-bank transfer (replaces MT202).
- **pacs.009 COVE**: Cover payment funding an underlying pacs.008 customer transfer (replaces MT202 COV).

## Key XML Elements
- **GrpHdr**: Group Header (MsgId, CreDtTm, NbOfTxs)
- **CdtTrfTxInf**: Financial Institution Transfer Details:
  - **PmtId/UETR**: Shared UUIDv4 tracking identifier
  - **IntrBkSttlmAmt**: Interbank settlement currency and amount
  - **InstgAgt & InstdAgt**: Instructing and Instructed Institutions
  - **UndrlygCstmrCdtTrf**: (COVE only) Underlying customer payment details`,
			Suggestions: []string{"Show pacs.009 XML", "Show pacs.009 XSD", "Compare pacs.008 vs pacs.009"},
			RelatedMsgs: matches,
		}
	}

	if strings.Contains(p, "pain.001") || strings.Contains(p, "pain001") || strings.Contains(p, "customer credit transfer initiation") {
		matches := e.Index.Search("pain.001")
		return MessageAnswer{
			Summary: "Customer Credit Transfer Initiation (pain.001)",
			Details: `**TL;DR**: **pain.001** (CustomerCreditTransferInitiation) is the client-to-bank instruction message sent by corporate or retail customers to instruct their debtor bank to execute payments.

## Core Capabilities
- **Batch Processing**: Supports multi-payment batches in a single file (PmtInf blocks).
- **Format Replacement**: Replaces proprietary corporate banking files & EDIFACT PAYMUL.
- **Interbank Pipeline**: Triggers downstream pacs.008 interbank clearing.

## Key XML Elements
- **GrpHdr**: Group Header (MsgId, CreDtTm, InitgPty)
- **PmtInf**: Payment Information (PmtInfId, Dbtr, DbtrAcct)
- **CdtTrfTxInf**: Individual payment transaction details (Cdtr, CdtrAcct, Amt, RmtInf)`,
			Suggestions: []string{"Show pain.001 XML", "Show pain.001 XSD", "What is pain.002?"},
			RelatedMsgs: matches,
		}
	}

	if strings.Contains(p, "camt.053") || strings.Contains(p, "camt053") || strings.Contains(p, "bank statement") {
		matches := e.Index.Search("camt.053")
		return MessageAnswer{
			Summary: "Bank-to-Customer Statement (camt.053)",
			Details: `### camt.053 — Bank-to-Customer Account Statement

**camt.053** provides official end-of-day or periodic account statements sent by account servicer banks to account owners (corporates, institutions).

#### Key Components:
- **` + "`Stmt`" + `**: Statement block with ` + "`Id`" + `, ` + "'CreDtTm'" + `, and ` + "`Acct`" + ` (IBAN / Account ID).
- **` + "`Bal`" + `**: Opening booked balance (` + "`OPBD`" + `) and Closing booked balance (` + "`CLBD`" + `).
- **` + "`Ntry`" + `**: Individual transaction entries with debit/credit indicators (` + "`CRDT`" + ` / ` + "`DBIT`" + `) and transaction breakdown.`,
			Suggestions: []string{"Show camt.053 XML", "Compare camt.052 vs camt.053", "Show camt.054 XML"},
			RelatedMsgs: matches,
		}
	}

	if strings.Contains(p, "bah") || strings.Contains(p, "business application header") || strings.Contains(p, "head.001") || strings.Contains(p, "head001") {
		matches := e.Index.Search("head.001")
		return MessageAnswer{
			Summary: "Business Application Header (head.001)",
			Details: `### Business Application Header (head.001)

The **Business Application Header (BAH)** is a standardized header defined in ` + "`head.001.001.01`" + ` / ` + "`head.001.001.02`" + ` that wraps ISO 20022 business payloads.

#### Core Elements:
- **` + "`Fr`" + ` (From)**: Sender organisation identifier (BICFI, Clearing Member).
- **` + "`To`" + ` (To)**: Recipient organisation identifier.
- **` + "`BizMsgIdr`" + `**: Unique business identifier for the transmission.
- **` + "`MsgDefIdr`" + `**: Specific payload message type (e.g. ` + "`pacs.008.001.10`" + `).
- **` + "`CreDt`" + `**: Timestamp of header creation.
- **` + "`CpyDpl`" + `**: Copy / Duplicate indicator (` + "`COPY`" + `, ` + "`DUPL`" + `, ` + "`CODU`" + `).
- **` + "`Sgntr`" + `**: Cryptographic digital signature for non-repudiation.`,
			Suggestions: []string{"Show head.001 XML", "Show head.001 XSD"},
			RelatedMsgs: matches,
		}
	}

	// 4. Extract any message code present in the query (e.g., "pacs.008", "pain.001", "camt.053")
	matches := msgCodeRegex.FindAllString(normPrompt, -1)
	if len(matches) > 0 {
		for _, m := range matches {
			normM := NormalizeQuery(m)
			results := e.Index.Search(normM)
			if len(results) > 0 {
				top := results[0]
				domainDesc := domainKeywords[top.BaseCode[:4]]
				if domainDesc == "" {
					domainDesc = top.Category
				}
				return MessageAnswer{
					Summary: fmt.Sprintf("%s — %s", top.ID, top.Category),
					Details: fmt.Sprintf("### %s\n\n- **Domain**: %s\n- **Category**: %s\n- **Release Version**: %s\n- **Base Message Code**: `%s`\n- **Schema Path**: `%s`\n- **Sample XML Path**: `%s`\n\nFound **%d** schema versions matching `%s` in the catalog.",
						top.ID, domainDesc, top.Category, top.Version, top.BaseCode, top.XSDPath, top.XMLSamplePath, len(results), normM),
					Suggestions: []string{
						fmt.Sprintf("Show %s XML", top.ID),
						fmt.Sprintf("Show %s XSD", top.ID),
					},
					RelatedMsgs: results,
				}
			}
		}
	}

	// 5. Semantic Domain Search across Categories
	for code, desc := range domainKeywords {
		if strings.Contains(p, code) || strings.Contains(p, strings.ToLower(desc)) {
			results := e.Index.Search(code)
			return MessageAnswer{
				Summary: fmt.Sprintf("%s Domain (%s)", strings.ToUpper(code), desc),
				Details: fmt.Sprintf("### ISO 20022 Domain: `%s` — %s\n\nContains **%d** message definitions across active versions.",
					strings.ToUpper(code), desc, len(results)),
				Suggestions: []string{
					fmt.Sprintf("Search %s messages", code),
					fmt.Sprintf("Show %s sample", code),
				},
				RelatedMsgs: results,
			}
		}
	}

	// 6. Direct Catalog Search Fallback
	cleanWords := []string{}
	stopWords := map[string]bool{"what": true, "is": true, "the": true, "how": true, "does": true, "work": true, "a": true, "an": true, "in": true, "for": true, "explain": true, "tell": true, "me": true, "about": true}
	for _, w := range strings.Fields(p) {
		clean := strings.Trim(w, "?!.,;:\"'")
		if !stopWords[clean] && len(clean) >= 2 {
			cleanWords = append(cleanWords, clean)
		}
	}

	searchKey := strings.Join(cleanWords, " ")
	if searchKey != "" {
		results := e.Index.Search(searchKey)
		if len(results) > 0 {
			top := results[0]
			return MessageAnswer{
				Summary: fmt.Sprintf("%s (%s)", top.ID, top.Category),
				Details: fmt.Sprintf("### %s\n\n- **Category**: %s\n- **Version**: %s\n- **Schema File**: `%s`\n- **Sample XML**: `%s`\n\nFound **%d** matching messages in the repository catalog.",
					top.ID, top.Category, top.Version, top.XSDPath, top.XMLSamplePath, len(results)),
				Suggestions: []string{fmt.Sprintf("Show %s XML", top.ID), fmt.Sprintf("Show %s XSD", top.ID)},
				RelatedMsgs: results,
			}
		}
	}

	// 7. General Knowledge Assistant Help
	return MessageAnswer{
		Summary: "AskISO Ask AI — ISO 20022 Assistant",
		Details: fmt.Sprintf("I could not find a specific message matching **%q**.\n\n"+
			"**Try asking:**\n"+
			"- **By Code**: `pacs.008`, `pain.001`, `camt.053`, `seev.031`, `head.001`\n"+
			"- **By Topic**: *Credit transfers*, *Direct debits*, *Account statements*, *Mandates*, *Corporate actions*\n"+
			"- **Comparisons**: *pacs.008 vs pacs.009*, *camt.052 vs camt.053*", rawPrompt),
		Suggestions: []string{
			"What is pacs.008?",
			"Compare pacs.008 vs pacs.009",
			"How does camt.053 work?",
			"Explain Business Application Header",
		},
	}
}

var (
	sharedTransport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	sharedHTTPClient = &http.Client{
		Transport: sharedTransport,
		Timeout:   5 * time.Second,
	}
)

func isValidEndpoint(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

const maxLLMResponse = 2 << 20

func (e *Engine) queryExternalLLM(prompt string) (string, bool, string) {
	var failures []string
	// 1. Check Ollama
	ollamaHost, ollamaConfigured := os.LookupEnv("OLLAMA_HOST")
	ollamaConfigured = ollamaConfigured && strings.TrimSpace(ollamaHost) != ""
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	if isValidEndpoint(ollamaHost) {
		if resp, ok := e.callOllama(ollamaHost, prompt); ok {
			return resp, true, ""
		}
		if ollamaConfigured {
			failures = append(failures, "the configured Ollama provider was unavailable or returned an invalid response")
		}
	} else if ollamaConfigured {
		failures = append(failures, "OLLAMA_HOST is not a valid HTTP(S) endpoint")
	}

	// 2. Check OpenAI Compatible API
	openaiKey := os.Getenv("OPENAI_API_KEY")
	if openaiKey != "" {
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if isValidEndpoint(baseURL) {
			if resp, ok := e.callOpenAI(openaiKey, baseURL, prompt); ok {
				return resp, true, ""
			}
			failures = append(failures, "the configured OpenAI-compatible provider was unavailable or returned an invalid response")
		} else {
			failures = append(failures, "OPENAI_BASE_URL is not a valid HTTP(S) endpoint")
		}
	}

	return "", false, strings.Join(failures, "; ")
}

func (e *Engine) callOllama(host, prompt string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/generate", strings.TrimRight(host, "/"))
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3"
	}
	payload := map[string]interface{}{
		"model":  model,
		"prompt": fmt.Sprintf("You are an expert ISO 20022 financial architect. Answer concisely with markdown formatting:\n\n%s", prompt),
		"stream": false,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponse+1))
	if err != nil || len(body) > maxLLMResponse {
		return "", false
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", false
	}
	return strings.TrimSpace(result.Response), true
}

func (e *Engine) callOpenAI(apiKey, baseURL, prompt string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/chat/completions", strings.TrimRight(baseURL, "/"))

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are AskISO Ask AI, an expert ISO 20022 financial messaging assistant. Answer accurately with clear markdown formatting."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponse+1))
	if err != nil || len(body) > maxLLMResponse {
		return "", false
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return "", false
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), true
}
