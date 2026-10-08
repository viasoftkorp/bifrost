package openai

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// openAIDecisionsPath is OpenAI's native decisions endpoint.
const openAIDecisionsPath = "/v1/decisions"

// ToOpenAIDecisionRequest converts a normalized decision request into the
// body of OpenAI's POST /v1/decisions. OpenAI takes text where the normalized
// shape also carries Typesafe's structured values, so a structured input,
// instruction, or description is sent as sorted JSON text, and a predicate's
// criteria are folded into its instructions. Input messages, including inline
// images, pass through unchanged.
//
// A request OpenAI cannot serve is rejected with a 400 for this attempt, so a
// fallback can take it: an empty (null) input, which OpenAI requires, and a
// question of an unsupported type or without its choices or levels.
func ToOpenAIDecisionRequest(request *schemas.BifrostDecisionRequest) (*OpenAIDecisionRequest, error) {
	if request == nil {
		return nil, providerUtils.InvalidRequestErrorf("decision request is nil")
	}
	if len(request.Questions) == 0 {
		return nil, providerUtils.InvalidRequestErrorf("decision request requires at least one question")
	}
	if err := request.Input.Validate(); err != nil {
		return nil, providerUtils.InvalidRequestErrorf("%s", err.Error())
	}
	if request.Input.IsEmpty() {
		return nil, providerUtils.InvalidRequestErrorf("OpenAI decisions require an input, and this request has none")
	}

	input := request.Input
	if input.Structured != nil {
		text, err := providerUtils.MarshalSorted(input.Structured)
		if err != nil {
			return nil, providerUtils.InvalidRequestErrorf("decision input could not be encoded as text: %v", err)
		}
		input = schemas.DecisionInput{Text: schemas.Ptr(string(text))}
	}

	questions := make([]schemas.DecisionQuestion, len(request.Questions))
	for i, question := range request.Questions {
		converted, err := toOpenAIDecisionQuestion(i, question)
		if err != nil {
			return nil, err
		}
		questions[i] = converted
	}

	return &OpenAIDecisionRequest{
		Model:            request.Model,
		Input:            input,
		Questions:        questions,
		SafetyIdentifier: request.SafetyIdentifier,
		ExtraParams:      request.ExtraParams,
	}, nil
}

// toOpenAIDecisionQuestion converts one question to the text-only form OpenAI
// accepts. A level without a label is labelled by its index, as the map form
// labels its levels.
func toOpenAIDecisionQuestion(index int, question schemas.DecisionQuestion) (schemas.DecisionQuestion, error) {
	if !question.Type.IsQuestionType() {
		return schemas.DecisionQuestion{}, providerUtils.InvalidRequestErrorf("question %d has unsupported type %q; expected predicate, choice, or score", index, question.Type)
	}
	instructions := strings.TrimSpace(providerUtils.DecisionTextString(question.Instructions) + providerUtils.DecisionCriteriaText(question.Criteria))
	converted := schemas.DecisionQuestion{
		Type:         question.Type,
		Name:         question.Name,
		Instructions: schemas.NewDecisionText(instructions),
	}

	switch question.Type {
	case schemas.DecisionTypeChoice:
		if len(question.Choices) == 0 {
			return schemas.DecisionQuestion{}, providerUtils.InvalidRequestErrorf("choice question %d requires choices", index)
		}
		converted.Choices = make([]schemas.DecisionChoice, len(question.Choices))
		for j, choice := range question.Choices {
			if _, err := choice.Key(); err != nil {
				return schemas.DecisionQuestion{}, providerUtils.InvalidRequestErrorf("choice %d of question %d: %s", j, index, err.Error())
			}
			converted.Choices[j] = schemas.DecisionChoice{Value: choice.Value, Description: toOpenAIText(choice.Description)}
		}
	case schemas.DecisionTypeScore:
		if len(question.Levels) == 0 {
			return schemas.DecisionQuestion{}, providerUtils.InvalidRequestErrorf("score question %d requires levels", index)
		}
		converted.Levels = make([]schemas.DecisionLevel, len(question.Levels))
		for j, level := range question.Levels {
			label := level.Label
			if label == "" {
				label = strconv.Itoa(j)
			}
			converted.Levels[j] = schemas.DecisionLevel{Label: label, Description: toOpenAIText(level.Description)}
		}
	}
	return converted, nil
}

// toOpenAIText returns text OpenAI accepts: text unchanged, and a structured
// value as sorted JSON text.
func toOpenAIText(text *schemas.DecisionText) *schemas.DecisionText {
	if text == nil || text.Structured == nil {
		return text
	}
	return schemas.NewDecisionText(providerUtils.DecisionTextString(text))
}

// ToBifrostDecisionResponse converts OpenAI's decisions response into the
// normalized shape. OpenAI answers the questions in order, so each answer is
// matched to the question at its position; one naming a different question, or
// answering with a different type, is an error so a fallback can take the
// request. A refusal and an answer of a type Bifrost does not model are kept
// as they are, and an answer without a name takes its question's.
func (response *OpenAIDecisionResponse) ToBifrostDecisionResponse(request *schemas.BifrostDecisionRequest) (*schemas.BifrostDecisionResponse, error) {
	if len(response.Answers) != len(request.Questions) {
		return nil, fmt.Errorf("OpenAI returned %d decision answers for %d questions", len(response.Answers), len(request.Questions))
	}
	answers := make([]schemas.DecisionAnswer, len(response.Answers))
	for i, wire := range response.Answers {
		answer := wire.DecisionAnswer
		question := request.Questions[i]
		if answer.Name != nil && question.Name != nil && *answer.Name != *question.Name {
			return nil, fmt.Errorf("OpenAI decision answer %d is named %q; expected %q", i, *answer.Name, *question.Name)
		}
		if answer.IsRecognized() && answer.Type != schemas.DecisionTypeRefusal && answer.Type != question.Type {
			return nil, fmt.Errorf("OpenAI answered decision question %d as %q; expected %q", i, answer.Type, question.Type)
		}
		if answer.Name == nil {
			answer.Name = question.Name
		}
		// OpenAI sends no legend; it is rebuilt from the question's levels so a
		// score answer reads the same whichever provider served it.
		if answer.Type == schemas.DecisionTypeScore && answer.Legend == nil {
			answer.Legend = providerUtils.DecisionScoreLegend(question)
		}
		answers[i] = answer
	}
	return &schemas.BifrostDecisionResponse{
		Model:   response.Model,
		Answers: answers,
		Usage:   response.Usage.ToBifrostLLMUsage(),
	}, nil
}

// ToBifrostDecisionRequest converts a request received on the
// /openai/v1/decisions route into the normalized shape. The input and
// questions are already the shared types, so they are copied as they are. A
// model without a provider prefix is OpenAI's, since this is OpenAI's route.
func (r *OpenAIDecisionRequest) ToBifrostDecisionRequest() *schemas.BifrostDecisionRequest {
	provider, model := schemas.ParseModelString(r.Model, schemas.OpenAI)
	return &schemas.BifrostDecisionRequest{
		Provider:         provider,
		Model:            model,
		Input:            r.Input,
		Questions:        r.Questions,
		SafetyIdentifier: r.SafetyIdentifier,
		Fallbacks:        schemas.ParseFallbacks(r.Fallbacks),
		ExtraParams:      r.ExtraParams,
	}
}

// ToOpenAIDecisionResponse renders a normalized decision response in OpenAI's
// shape for the /openai/v1/decisions route, whichever provider answered, so
// what plugins did to the response is what the client receives. Unnamed
// answers carry name null as OpenAI's do; Laya's answer, usage, and routing
// fields come along when Laya answered, as fields OpenAI clients ignore.
func ToOpenAIDecisionResponse(response *schemas.BifrostDecisionResponse) *OpenAIDecisionResponse {
	if response == nil {
		return nil
	}
	answers := make([]OpenAIDecisionAnswer, len(response.Answers))
	for i, answer := range response.Answers {
		answers[i] = OpenAIDecisionAnswer{DecisionAnswer: answer}
	}
	return &OpenAIDecisionResponse{
		Model:   response.Model,
		Answers: answers,
		Usage:   toOpenAIDecisionUsage(response.Usage),
		Routing: response.Routing,
	}
}

// HandleOpenAIDecisionRequest sends a decision request to an OpenAI-compatible
// POST /v1/decisions and converts the reply. The reply is buffered and parsed
// in-process, as for rerank: it is a short list of answers, and large-response
// passthrough would relay upstream bytes verbatim to routes that do not speak
// OpenAI's shape.
func HandleOpenAIDecisionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostDecisionRequest,
	key schemas.Key,
	extraHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostDecisionResponse, *schemas.BifrostError) {
	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIDecisionRequest(request)
		},
	)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	for k, v := range BearerAuthHeader(key) {
		req.Header.Set(k, v)
	}
	// A nil body means large-payload passthrough staged the request as a
	// stream; apply it instead of sending an empty body.
	if !providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, providerName) {
		req.SetBody(jsonData)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, _, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response := &OpenAIDecisionResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	bifrostResponse, err := response.ToBifrostDecisionResponse(request)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(err.Error(), nil), jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if bifrostResponse.Model == "" {
		bifrostResponse.Model = request.Model
	}
	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders
	if sendBackRawRequest {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}
