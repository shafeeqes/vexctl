/*
Copyright 2022 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package vex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"chainguard.dev/vex/pkg/csaf"
)

const (
	// formatIdentifier is the identifier value used in a VEX document's Format field.
	formatIdentifier = "text/vex+json"

	// TypeURI is the type used to describe VEX documents, e.g. within [in-toto
	// statements].
	//
	// [in-toto statements]: https://github.com/in-toto/attestation/blob/main/spec/README.md#statement
	TypeURI = "text/vex"

	// DefaultAuthor is the default value for a document's Author field.
	DefaultAuthor = "Unknown Author"

	// DefaultRole is the default value for a document's AuthorRole field.
	DefaultRole = "Document Creator"
)

// The VEX type represents a VEX document and all of its contained information.
type VEX struct {
	Metadata
	Statements []Statement `json:"statements"`
}

// The Metadata type represents the metadata associated with a VEX document.
type Metadata struct {
	// ID is the identifying string for the VEX document. This should be unique per
	// document.
	ID string `json:"id"`

	// Format describes the format of this VEX document.
	Format string `json:"format"`

	// Author is the identifier for the author of the VEX statement, ideally a common
	// name, may be a URI. [author] is an individual or organization. [author]
	// identity SHOULD be cryptographically associated with the signature of the VEX
	// statement or document or transport.
	Author string `json:"author"`

	// AuthorRole describes the role of the document Author.
	AuthorRole string `json:"role"`

	// Timestamp defines the time at which the document was issued.
	Timestamp *time.Time `json:"timestamp"`
}

// VulnerabilityReference captures other identifier assigned to the CVE.
type VulnerabilityReference struct {
	RefType   string `json:"type"` // URL, OSV, FEDORA, etc
	Reference string `reference:"ref"`
}

// New returns a new, initialized VEX document.
func New() VEX {
	now := time.Now()
	return VEX{
		Metadata: Metadata{
			Format:    formatIdentifier,
			Timestamp: &now,
		},
		Statements: []Statement{},
	}
}

// Load reads the VEX document file at the given path and returns a decoded VEX
// object. If Load is unable to read the file or decode the document, it returns
// an error.
func Load(path string) (*VEX, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading VEX file: %w", err)
	}

	vexDoc := &VEX{}
	if err := json.Unmarshal(data, vexDoc); err != nil {
		return nil, fmt.Errorf("unmarshaling VEX document: %w", err)
	}
	return vexDoc, nil
}

// OpenYAML opens a VEX file in YAML format.
func OpenYAML(path string) (*VEX, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening YAML file: %w", err)
	}
	vexDoc := New()
	if err := yaml.Unmarshal(data, &vexDoc); err != nil {
		return nil, fmt.Errorf("unmarshalling VEX data: %w", err)
	}
	return &vexDoc, nil
}

// OpenJSON opens a VEX file in JSON format.
func OpenJSON(path string) (*VEX, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening JSON file: %w", err)
	}
	vexDoc := New()
	if err := json.Unmarshal(data, &vexDoc); err != nil {
		return nil, fmt.Errorf("unmarshalling VEX data: %w", err)
	}
	return &vexDoc, nil
}

// ToJSON serializes the VEX document to JSON and writes it to the passed writer.
func (vexDoc *VEX) ToJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(vexDoc); err != nil {
		return fmt.Errorf("encoding vex document: %w", err)
	}
	return nil
}

// StatementFromID returns a statement for a given vulnerability if there is one.
func (vexDoc *VEX) StatementFromID(id string) *Statement {
	for _, statement := range vexDoc.Statements { //nolint:gocritic // turning off for rule rangeValCopy
		if statement.Vulnerability == id {
			logrus.Infof("VEX doc contains statement for CVE %s", id)
			return &statement
		}
	}
	return nil
}

// SortDocuments sorts and returns a slice of documents based on their date.
// VEXes should be applied sequentially in chronological order as they capture
// knowledge about an artifact as it changes over time.
func SortDocuments(docs []*VEX) []*VEX {
	sort.Slice(docs, func(i, j int) bool {
		if docs[j].Timestamp == nil {
			return true
		}
		if docs[i].Timestamp == nil {
			return false
		}
		return docs[i].Timestamp.Before(*(docs[j].Timestamp))
	})
	return docs
}

// OpenCSAF opens a CSAF document and builds a VEX object from it.
func OpenCSAF(path string, products []string) (*VEX, error) {
	csafDoc, err := csaf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening csaf doc: %w", err)
	}

	productDict := map[string]string{}
	for _, pid := range products {
		productDict[pid] = pid
	}

	// If no products were specified, we use the first one
	if len(products) == 0 {
		p := csafDoc.FirstProductName()
		if p == "" {
			// Error? I think so.
			return nil, errors.New("unable to find a product ID in CSAF document")
		}
		productDict[p] = p
	}

	// Create the vex doc
	v := &VEX{
		Metadata: Metadata{
			ID:         csafDoc.Document.Tracking.ID,
			Author:     "",
			AuthorRole: "",
			Timestamp:  &time.Time{},
		},
		Statements: []Statement{},
	}

	// Cycle the CSAF vulns list and get those that apply
	for _, c := range csafDoc.Vulnerabilities {
		for status, docProducts := range c.ProductStatus {
			for _, productID := range docProducts {
				if _, ok := productDict[productID]; ok {
					// Check we have a valid status
					if StatusFromCSAF(status) == "" {
						return nil, fmt.Errorf("invalid status for product %s", productID)
					}

					// TODO search the threats struct for justification, etc
					just := ""
					for _, t := range c.Threats {
						// Search the threats for a justification
						for _, p := range t.ProductIDs {
							if p == productID {
								just = t.Details
							}
						}
					}

					v.Statements = append(v.Statements, Statement{
						Vulnerability:   c.CVE,
						Status:          StatusFromCSAF(status),
						Justification:   "", // Justifications are not machine readable in csaf, it seems
						ActionStatement: just,
						Products:        products,
					})
				}
			}
		}
	}

	return v, nil
}
