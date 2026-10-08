package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/core/providers/typesafe"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// The map form is the deprecated /v1/decisions body: a state plus questions
// keyed by name, answered by answers keyed by the same names. Its questions
// are Typesafe's System One questions (written with "kind" for "type"), so it
// is decoded and converted through the Typesafe package. It stays served for
// callers written against it; new callers send the normalized body.

// DecisionMapFormRequest is a /v1/decisions request in the deprecated map
// form.
type DecisionMapFormRequest struct {
	State     interface{}                          `json:"state"`
	Questions map[string]typesafe.TypesafeQuestion `json:"questions"`
	BifrostParams
}

// DecisionMapFormResponse is the response to a map-form request: answers keyed
// by question name.
type DecisionMapFormResponse struct {
	ID          string                             `json:"id,omitempty"`
	Model       string                             `json:"model"`
	Answers     map[string]DecisionMapAnswer       `json:"answers"`
	Usage       *schemas.BifrostLLMUsage           `json:"usage,omitempty"`
	ExtraFields schemas.BifrostResponseExtraFields `json:"extra_fields"`

	// Laya-specific fields
	Routing json.RawMessage `json:"routing,omitempty"`
}

// DecisionMapAnswer is one answer of a map-form response. Value is the noul
// probability, the chosen option, or the score; a refusal has no value. An
// answer of a type Bifrost does not model is re-emitted verbatim.
type DecisionMapAnswer struct {
	Kind          string             `json:"kind"`
	Value         interface{}        `json:"value"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]any     `json:"legend,omitempty"`

	// Laya-specific fields
	AnswerConfidence    *float64        `json:"answer_confidence,omitempty"`
	Action              json.RawMessage `json:"action,omitempty"`
	Abstention          *string         `json:"abstention,omitempty"`
	AbstentionThreshold *float64        `json:"abstention_threshold,omitempty"`
	LowConfidence       *bool           `json:"low_confidence,omitempty"`

	raw json.RawMessage // verbatim answer of a type Bifrost does not model
}

// MarshalJSON re-emits an unrecognized answer verbatim and otherwise marshals
// the typed fields with sorted keys, the encoding SendJSON uses, so the
// response renders as it always has.
func (a DecisionMapAnswer) MarshalJSON() ([]byte, error) {
	if a.raw != nil {
		return a.raw, nil
	}
	type alias DecisionMapAnswer
	return schemas.MarshalSorted(alias(a))
}

var decisionMapFormKnownFields = map[string]bool{
	"model":     true,
	"state":     true,
	"questions": true,
	"fallbacks": true,
}

// prepareMapFormDecisionRequest prepares a BifrostDecisionRequest from a
// map-form body: the state becomes the input and the questions are normalized
// through the Typesafe converter.
func prepareMapFormDecisionRequest(ctx *fasthttp.RequestCtx, config *lib.Config) (*schemas.BifrostDecisionRequest, error) {
	req, base, err := prepareRequest[DecisionMapFormRequest](ctx, config, decisionMapFormKnownFields)
	if err != nil {
		return nil, err
	}
	// An explicit null state is SDK-valid and forwarded; only an absent key is
	// rejected here.
	if req.State == nil && !gjson.GetBytes(ctx.PostBody(), "state").Exists() {
		return nil, fmt.Errorf("state is required for decision")
	}
	if len(req.Questions) == 0 {
		return nil, fmt.Errorf("questions are required for decision")
	}
	questions, err := typesafe.ToBifrostDecisionQuestions(req.Questions)
	if err != nil {
		return nil, err
	}
	return &schemas.BifrostDecisionRequest{
		Provider:    base.Provider,
		Model:       base.ModelName,
		Input:       typesafe.ToBifrostDecisionInput(req.State),
		Questions:   questions,
		Fallbacks:   base.Fallbacks,
		ExtraParams: base.ExtraParams,
	}, nil
}

// toDecisionMapFormResponse renders a normalized response in the map form,
// through Typesafe's answer shape: each answer's noul, choice, or score
// becomes its value.
func toDecisionMapFormResponse(resp *schemas.BifrostDecisionResponse) *DecisionMapFormResponse {
	if resp == nil {
		return nil
	}
	answers := make(map[string]DecisionMapAnswer, len(resp.Answers))
	for name, native := range typesafe.ToTypesafeAnswers(resp.Answers) {
		if raw := native.RawJSON(); raw != nil {
			answers[name] = DecisionMapAnswer{Kind: native.Type, raw: raw}
			continue
		}
		answer := DecisionMapAnswer{
			Kind:                native.Type,
			Confidence:          native.Confidence,
			Probabilities:       native.Probabilities,
			Legend:              native.Legend,
			AnswerConfidence:    native.AnswerConfidence,
			Action:              native.Action,
			Abstention:          native.Abstention,
			AbstentionThreshold: native.AbstentionThreshold,
			LowConfidence:       native.LowConfidence,
		}
		switch {
		case native.Noul != nil:
			answer.Value = *native.Noul
		case native.Choice != nil:
			answer.Value = *native.Choice
		case native.Score != nil:
			answer.Value = *native.Score
		}
		answers[name] = answer
	}
	return &DecisionMapFormResponse{
		ID:          resp.ID,
		Model:       resp.Model,
		Answers:     answers,
		Usage:       resp.Usage,
		ExtraFields: resp.ExtraFields,
		Routing:     resp.Routing,
	}
}
