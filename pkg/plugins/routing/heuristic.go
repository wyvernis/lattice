package routing

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/lattice-ai/lattice/pkg/types"
	"gopkg.in/yaml.v3"
)

// Category labels used by the default classifier.
const (
	CatCoding        = "coding"
	CatReasoning     = "reasoning"
	CatSummarization = "summarization"
	CatTranslation   = "translation"
	CatVision        = "vision"
	CatChat          = "chat"
	CatEmbedding     = "embedding"
)

// PolicyConfig maps categories to model preference lists.
type PolicyConfig struct {
	DefaultPolicy types.RoutingPolicy     `yaml:"default_policy" json:"default_policy"`
	Routes        map[string][]string     `yaml:"routes" json:"routes"`
	Overrides     map[string]string       `yaml:"overrides" json:"overrides"`
}

// DefaultPolicy returns built-in category → models mapping.
func DefaultPolicy() PolicyConfig {
	return PolicyConfig{
		DefaultPolicy: types.PolicyBalanced,
		Routes: map[string][]string{
			CatCoding:        {"qwen2.5-coder-7b", "deepseek-coder-6.7b", "codellama-7b"},
			CatReasoning:     {"deepseek-r1-7b", "qwen2.5-32b", "llama3.1-8b"},
			CatSummarization: {"mistral-7b", "llama3.1-8b", "phi-3-mini"},
			CatTranslation:   {"aya-23-8b", "mistral-7b", "nllb-200"},
			CatVision:        {"qwen2-vl-7b", "llava-1.6-7b"},
			CatChat:          {"llama3.1-8b", "mistral-7b", "phi-3-mini"},
			CatEmbedding:     {"nomic-embed-text", "bge-small-en"},
		},
	}
}

// HeuristicRouter classifies requests with keyword / regex heuristics.
type HeuristicRouter struct {
	mu     sync.RWMutex
	policy PolicyConfig
	rules  []rule
}

type rule struct {
	category string
	weight   float64
	re       *regexp.Regexp
}

// NewHeuristicRouter constructs the default intelligent router.
func NewHeuristicRouter(policy PolicyConfig) *HeuristicRouter {
	if policy.Routes == nil {
		policy = DefaultPolicy()
	}
	r := &HeuristicRouter{policy: policy}
	r.rules = []rule{
		{CatCoding, 1.0, regexp.MustCompile(`(?i)\b(code|function|bug|compile|refactor|python|golang|typescript|sql|algorithm|leetcode)\b`)},
		{CatReasoning, 0.95, regexp.MustCompile(`(?i)\b(reason|prove|step by step|logic|math|theorem|derive|chain of thought)\b`)},
		{CatSummarization, 0.9, regexp.MustCompile(`(?i)\b(summar(y|ize)|tl;dr|tldr|key points|abstract)\b`)},
		{CatTranslation, 0.9, regexp.MustCompile(`(?i)\b(translate|translation|from \w+ to \w+|en→|fr→|es→)\b`)},
		{CatVision, 1.0, regexp.MustCompile(`(?i)\b(image|picture|photo|screenshot|describe this|vision|ocr)\b`)},
		{CatEmbedding, 1.0, regexp.MustCompile(`(?i)\b(embed|embedding|vectorize|similarity)\b`)},
	}
	return r
}

func (r *HeuristicRouter) Name() string { return "heuristic" }

// Classify scores the request text against rules.
func (r *HeuristicRouter) Classify(ctx context.Context, req *types.InferenceRequest) (*types.Classification, error) {
	if req.Type == types.RequestEmbedding {
		return &types.Classification{Category: CatEmbedding, Confidence: 1, Scores: map[string]float64{CatEmbedding: 1}}, nil
	}
	text := req.Prompt
	for _, m := range req.Messages {
		text += " " + m.Content
	}
	text = strings.TrimSpace(text)
	scores := map[string]float64{}
	best, bestScore := CatChat, 0.15
	for _, rule := range r.rules {
		if rule.re.MatchString(text) {
			scores[rule.category] = rule.weight
			if rule.weight > bestScore {
				best, bestScore = rule.category, rule.weight
			}
		}
	}
	if len(scores) == 0 {
		scores[CatChat] = bestScore
	}
	return &types.Classification{Category: best, Confidence: bestScore, Scores: scores}, nil
}

// CandidateModels returns ordered model candidates for a classification.
func (r *HeuristicRouter) CandidateModels(ctx context.Context, class *types.Classification, policy types.RoutingPolicy) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if class == nil {
		return r.policy.Routes[CatChat], nil
	}
	if override, ok := r.policy.Overrides[class.Category]; ok && override != "" {
		return []string{override}, nil
	}
	models := append([]string{}, r.policy.Routes[class.Category]...)
	if len(models) == 0 {
		models = append([]string{}, r.policy.Routes[CatChat]...)
	}
	_ = policy // cost/quality reordering happens in cost engine
	return models, nil
}

// UpdatePolicy hot-reloads routing config.
func (r *HeuristicRouter) UpdatePolicy(p PolicyConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = p
}

// Policy returns a copy of current policy.
func (r *HeuristicRouter) Policy() PolicyConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy
}

// LoadPolicyYAML parses YAML policy bytes.
func LoadPolicyYAML(data []byte) (PolicyConfig, error) {
	p := DefaultPolicy()
	if err := yaml.Unmarshal(data, &p); err != nil {
		return p, err
	}
	return p, nil
}
