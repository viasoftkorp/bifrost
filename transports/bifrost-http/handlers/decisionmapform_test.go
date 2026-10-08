package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// TestPrepareDecisionRequestPicksBodyForm pins how /v1/decisions reads its two
// bodies: the normalized body first, and the deprecated map form when that
// does not decode or the body carries "state". A body with both "state" and
// "input" is rejected.
func TestPrepareDecisionRequestPicksBodyForm(t *testing.T) {
	const model = `"model":"typesafe/jev-1.13.0",`
	cases := map[string]struct {
		body    string
		mapForm bool
		wantErr string
	}{
		"normalized":               {body: `{` + model + `"input":"s","questions":[{"type":"predicate","name":"q"}]}`},
		"map form":                 {body: `{` + model + `"state":"s","questions":{"q":{"kind":"noul"}}}`, mapForm: true},
		"map form without state":   {body: `{` + model + `"questions":{"q":{"kind":"noul"}}}`, wantErr: "state is required"},
		"state without questions":  {body: `{` + model + `"state":"s"}`, wantErr: "questions are required"},
		"normalized without input": {body: `{` + model + `"questions":[{"type":"predicate","name":"q"}]}`, wantErr: "input is required"},
		"malformed normalized":     {body: `{` + model + `"input":7,"questions":[{"type":"predicate","name":"q"}]}`, wantErr: "Invalid request payload"},
		"state with input":         {body: `{` + model + `"state":"s","input":"s","questions":[{"type":"predicate"}]}`, wantErr: "both input and the deprecated state"},
		"state with a list":        {body: `{` + model + `"state":"s","questions":[{"type":"predicate"}]}`, wantErr: "Invalid request payload"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetBodyString(tc.body)
			_, mapForm, err := prepareDecisionRequest(ctx, nil)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.mapForm, mapForm)
		})
	}
}

// TestPrepareMapFormDecisionRequestNormalizes pins that a map-form body
// becomes the normalized request: questions sorted by name and named after
// their keys, noul as predicate keeping its criteria, options as choices, and
// score levels labelled by index, with the state as the input.
func TestPrepareMapFormDecisionRequestNormalizes(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetBodyString(`{"model":"typesafe/jev-1.13.0","state":{"ticket":"refund"},"questions":{
		"urgency":{"kind":"score","instructions":"How urgent?","criteria":["low","high"]},
		"angry":{"kind":"noul","instructions":"Angry?","criteria":{"true":"upset"}},
		"category":{"kind":"choice","criteria":{"billing":"money","bug":null}}
	}}`)
	req, err := prepareMapFormDecisionRequest(ctx, nil)
	require.NoError(t, err)

	assert.Equal(t, schemas.Typesafe, req.Provider)
	assert.Equal(t, map[string]any{"ticket": "refund"}, req.Input.Structured)
	require.Len(t, req.Questions, 3)
	angry, category, urgency := req.Questions[0], req.Questions[1], req.Questions[2]
	assert.Equal(t, "angry", *angry.Name)
	assert.Equal(t, schemas.DecisionTypePredicate, angry.Type)
	assert.Equal(t, &schemas.DecisionCriteria{True: schemas.NewDecisionText("upset")}, angry.Criteria)
	require.Len(t, category.Choices, 2)
	assert.Equal(t, "billing", *category.Choices[0].Value.Str)
	assert.Nil(t, category.Choices[1].Description)
	require.Len(t, urgency.Levels, 2)
	assert.Equal(t, schemas.DecisionLevel{Label: "1", Description: schemas.NewDecisionText("high")}, urgency.Levels[1])

	bad := &fasthttp.RequestCtx{}
	bad.Request.SetBodyString(`{"model":"typesafe/jev-1.13.0","state":"s","questions":{"q":{"kind":"ranking"}}}`)
	_, err = prepareMapFormDecisionRequest(bad, nil)
	assert.ErrorContains(t, err, "unsupported kind")
}

// TestPrepareDecisionRequestReadsNormalizedBody pins the normalized body: a
// string input is text, an array is messages, an object is structured input,
// and questions keep their order and list-only details.
func TestPrepareDecisionRequestReadsNormalizedBody(t *testing.T) {
	questions := `"questions":[
		{"type":"choice","name":"refund","instructions":"Refund?","choices":[{"value":true,"description":"yes"},{"value":false}]},
		{"type":"score","instructions":"Rate","levels":[{"label":"Low"},{"label":"High","description":"urgent"}]}
	]`
	for name, tc := range map[string]struct {
		input string
		check func(t *testing.T, input schemas.DecisionInput)
	}{
		"text": {`"I was charged twice."`, func(t *testing.T, input schemas.DecisionInput) {
			assert.Equal(t, "I was charged twice.", *input.Text)
		}},
		"messages": {`[{"role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]`, func(t *testing.T, input schemas.DecisionInput) {
			require.Len(t, input.Messages, 1)
			assert.Equal(t, schemas.DecisionInputPartTypeImage, input.NonTextPartType())
		}},
		"structured": {`{"ticket":"refund"}`, func(t *testing.T, input schemas.DecisionInput) {
			assert.Equal(t, map[string]interface{}{"ticket": "refund"}, input.Structured)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetBodyString(`{"model":"typesafe/jev-1.13.0","safety_identifier":"user-1","input":` + tc.input + `,` + questions + `}`)
			req, mapForm, err := prepareDecisionRequest(ctx, nil)
			require.NoError(t, err)
			assert.False(t, mapForm)
			tc.check(t, req.Input)
			assert.Equal(t, "user-1", *req.SafetyIdentifier)
			require.Len(t, req.Questions, 2)
			assert.True(t, *req.Questions[0].Choices[0].Value.Bool)
			assert.Nil(t, req.Questions[1].Name)
			assert.Equal(t, "High", req.Questions[1].Levels[1].Label)
		})
	}
}

// TestDecisionMapFormResponseWire pins the exact map-form response callers
// parse: answers keyed by name with kind and value, probabilities keyed by
// option or level index, and Laya fields under their own names. A refusal
// keeps its kind with a null value, and an unrecognized answer is re-emitted
// verbatim. The payloads are synthetic.
func TestDecisionMapFormResponseWire(t *testing.T) {
	var unknown schemas.DecisionAnswer
	require.NoError(t, schemas.Unmarshal([]byte(`{"type":"ranking","name":"future","order":["a","b"]}`), &unknown))
	answerConfidence := 0.97
	resp := &schemas.BifrostDecisionResponse{
		Model: "jev-1.13.0",
		Answers: []schemas.DecisionAnswer{
			{Type: schemas.DecisionTypePredicate, Name: schemas.Ptr("angry"), Probability: schemas.Ptr(0.25)},
			{Type: schemas.DecisionTypeChoice, Name: schemas.Ptr("category"), Choice: &schemas.DecisionScalar{Str: schemas.Ptr("billing")}, Confidence: schemas.Ptr(0.8),
				Probabilities: []schemas.DecisionProbability{
					{Value: schemas.DecisionScalar{Str: schemas.Ptr("billing")}, Probability: 0.9},
					{Value: schemas.DecisionScalar{Str: schemas.Ptr("bug")}, Probability: 0.1},
				}, AnswerConfidence: &answerConfidence},
			{Type: schemas.DecisionTypeScore, Name: schemas.Ptr("urgency"), Score: schemas.Ptr(1.5),
				Probabilities: []schemas.DecisionProbability{
					{Value: schemas.DecisionScalar{Num: schemas.Ptr(0.0)}, Label: schemas.Ptr("0"), Probability: 0.5},
					{Value: schemas.DecisionScalar{Num: schemas.Ptr(2.0)}, Label: schemas.Ptr("2"), Probability: 0.5},
				}, Legend: map[string]any{"0": "low", "2": "high"}},
			{Type: schemas.DecisionTypeRefusal, Name: schemas.Ptr("declined")},
			unknown,
		},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 3, TotalTokens: 3},
	}

	encoded, err := schemas.MarshalSorted(toDecisionMapFormResponse(resp))
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.ElementsMatch(t, []string{"model", "answers", "usage", "extra_fields"}, keysOf(wire))
	assert.Equal(t, map[string]any{
		"angry":    map[string]any{"kind": "noul", "value": 0.25},
		"category": map[string]any{"kind": "choice", "value": "billing", "confidence": 0.8, "probabilities": map[string]any{"billing": 0.9, "bug": 0.1}, "answer_confidence": 0.97},
		"urgency":  map[string]any{"kind": "score", "value": 1.5, "probabilities": map[string]any{"0": 0.5, "2": 0.5}, "legend": map[string]any{"0": "low", "2": "high"}},
		"declined": map[string]any{"kind": "refusal", "value": nil},
		"future":   map[string]any{"type": "ranking", "name": "future", "order": []any{"a", "b"}},
	}, wire["answers"])
	assert.False(t, strings.Contains(string(encoded), `"type":"noul"`), "the map form reports kind, not Typesafe's type")
	assert.Nil(t, toDecisionMapFormResponse(nil))
}

// keysOf returns a map's keys.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
