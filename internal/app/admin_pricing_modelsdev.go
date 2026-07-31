package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
)

const (
	modelsDevAPIURL          = "https://models.dev/catalog.json"
	modelsDevFetchTimeout    = 20 * time.Second
	modelsDevCacheTTL        = 15 * time.Minute
	modelsDevMaxResponseSize = 64 << 20
	modelsDevMaxImportKeys   = 500
)

type modelsDevCostFields struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type modelsDevCostTier struct {
	modelsDevCostFields
	Tier struct {
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tier"`
}

type modelsDevCost struct {
	modelsDevCostFields
	ContextOver200K *modelsDevCostFields `json:"context_over_200k"`
	Tiers           []modelsDevCostTier  `json:"tiers"`
}

type modelsDevModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Family      string `json:"family"`
	Lab         string `json:"lab"`
	BaseModel   string `json:"base_model"`
	ReleaseDate string `json:"release_date"`
	Status      string `json:"status"`
	Modalities  struct {
		Output []string `json:"output"`
	} `json:"modalities"`
	Cost *modelsDevCost `json:"cost"`
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevCatalogPayload struct {
	Models    map[string]modelsDevModel    `json:"models"`
	Providers map[string]modelsDevProvider `json:"providers"`
}

// ModelsDevCatalogEntry 是提供给管理页面的精简定价目录。
type ModelsDevCatalogEntry struct {
	Key                 string  `json:"key"`
	ProviderID          string  `json:"provider_id"`
	ProviderName        string  `json:"provider_name"`
	LabID               string  `json:"lab_id,omitempty"`
	LabName             string  `json:"lab_name,omitempty"`
	AssignmentKey       string  `json:"assignment_key"`
	ModelID             string  `json:"model_id"`
	NormalizedModelID   string  `json:"normalized_model_id"`
	DisplayName         string  `json:"display_name"`
	ChannelType         string  `json:"channel_type"`
	ReleaseDate         string  `json:"release_date,omitempty"`
	InputPrice          float64 `json:"input_price"`
	OutputPrice         float64 `json:"output_price"`
	CacheReadPrice      float64 `json:"cache_read_price"`
	CacheWritePrice     float64 `json:"cache_write_price"`
	InputPriceHigh      float64 `json:"input_price_high"`
	OutputPriceHigh     float64 `json:"output_price_high"`
	CacheReadPriceHigh  float64 `json:"cache_read_price_high"`
	CacheWritePriceHigh float64 `json:"cache_write_price_high"`
	HighPriceThreshold  int64   `json:"high_price_threshold"`
	NeedsChannelType    bool    `json:"needs_channel_type"`
	Exists              bool    `json:"exists"`
}

type modelsDevImportRequest struct {
	Keys         []string          `json:"keys"`
	Overwrite    bool              `json:"overwrite"`
	ChannelTypes map[string]string `json:"channel_types"`
}

// HandleListModelsDevPricing 从 models.dev 获取可选择的模型定价。
// GET /admin/pricing/models-dev
func (s *Server) HandleListModelsDevPricing(c *gin.Context) {
	forceRefresh := c.Query("refresh") == "1"
	entries, fetchedAt, err := s.loadModelsDevCatalog(c.Request.Context(), forceRefresh)
	if err != nil {
		log.Printf("[ERROR] models.dev catalog fetch failed: %v", err)
		RespondErrorMsg(c, http.StatusBadGateway, fmt.Sprintf("获取 models.dev 定价失败: %v", err))
		return
	}

	existing, err := s.store.ListModelPricing(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	markExistingModelsDevEntries(entries, existing)

	RespondJSON(c, http.StatusOK, gin.H{
		"entries":    entries,
		"fetched_at": fetchedAt.UnixMilli(),
		"source_url": modelsDevAPIURL,
	})
}

// HandleImportModelsDevPricing 仅导入用户明确选择的 models.dev 定价。
// POST /admin/pricing/models-dev/import
func (s *Server) HandleImportModelsDevPricing(c *gin.Context) {
	var req modelsDevImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if len(req.Keys) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "请至少选择一个模型")
		return
	}
	if len(req.Keys) > modelsDevMaxImportKeys {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("单次最多导入 %d 个模型", modelsDevMaxImportKeys))
		return
	}

	catalog, _, err := s.loadModelsDevCatalog(c.Request.Context(), false)
	if err != nil {
		log.Printf("[ERROR] models.dev import catalog fetch failed: %v", err)
		RespondErrorMsg(c, http.StatusBadGateway, fmt.Sprintf("获取 models.dev 定价失败: %v", err))
		return
	}
	existing, err := s.store.ListModelPricing(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	entries, result, err := buildModelsDevImportEntries(catalog, existing, req.Keys, req.Overwrite, req.ChannelTypes)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(entries) > 0 {
		if _, err := s.store.BatchCreateModelPricing(c.Request.Context(), entries); err != nil {
			log.Printf("[ERROR] models.dev pricing import failed: %v", err)
			RespondError(c, http.StatusInternalServerError, err)
			return
		}
		s.refreshPricingCache()
	}

	log.Printf("[INFO] models.dev pricing import: created=%d updated=%d skipped=%d missing=%d duplicate=%d",
		result.Created, result.Updated, result.Skipped, result.Missing, result.Duplicate)
	RespondJSON(c, http.StatusOK, gin.H{
		"message":   fmt.Sprintf("models.dev 同步完成：新增 %d，更新 %d，跳过 %d", result.Created, result.Updated, result.Skipped),
		"created":   result.Created,
		"updated":   result.Updated,
		"skipped":   result.Skipped,
		"missing":   result.Missing,
		"duplicate": result.Duplicate,
	})
}

func (s *Server) loadModelsDevCatalog(parent context.Context, force bool) ([]ModelsDevCatalogEntry, time.Time, error) {
	s.modelsDevMu.Lock()
	defer s.modelsDevMu.Unlock()

	if !force && len(s.modelsDevCatalog) > 0 && time.Since(s.modelsDevFetchedAt) < modelsDevCacheTTL {
		return append([]ModelsDevCatalogEntry(nil), s.modelsDevCatalog...), s.modelsDevFetchedAt, nil
	}

	ctx, cancel := context.WithTimeout(parent, modelsDevFetchTimeout)
	defer cancel()
	entries, err := fetchModelsDevCatalog(ctx, s.client, modelsDevAPIURL)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.modelsDevCatalog = append([]ModelsDevCatalogEntry(nil), entries...)
	s.modelsDevFetchedAt = time.Now()
	return entries, s.modelsDevFetchedAt, nil
}

func fetchModelsDevCatalog(ctx context.Context, client *http.Client, sourceURL string) ([]ModelsDevCatalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ccLoad/models.dev-pricing-sync")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	limited := io.LimitReader(resp.Body, modelsDevMaxResponseSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(data) > modelsDevMaxResponseSize {
		return nil, fmt.Errorf("response exceeds %d bytes", modelsDevMaxResponseSize)
	}

	var payload modelsDevCatalogPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// 兼容 models.dev 旧版 api.json 形状，便于镜像或缓存尚未切换时继续工作。
	if len(payload.Providers) == 0 {
		var providers map[string]modelsDevProvider
		if err := json.Unmarshal(data, &providers); err != nil {
			return nil, fmt.Errorf("decode legacy response: %w", err)
		}
		payload.Providers = providers
	}
	return flattenModelsDevCatalog(payload), nil
}

func flattenModelsDevCatalog(payload modelsDevCatalogPayload) []ModelsDevCatalogEntry {
	resolver := newModelsDevLabResolver(payload.Models, payload.Providers)
	entries := make([]ModelsDevCatalogEntry, 0)
	for providerKey, provider := range payload.Providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			providerID = providerKey
		}
		providerName := strings.TrimSpace(provider.Name)
		if providerName == "" {
			providerName = providerID
		}

		for modelKey, sourceModel := range provider.Models {
			modelID := strings.TrimSpace(sourceModel.ID)
			if modelID == "" {
				modelID = modelKey
			}
			if !isModelsDevTextPricingModel(modelID, sourceModel) {
				continue
			}
			normalizedID := normalizeModelsDevModelID(modelID)
			if normalizedID == "" {
				continue
			}
			labID := resolver.resolve(providerID, modelID, normalizedID, sourceModel)
			channelType, matched := modelsDevChannelTypeForLab(labID)
			assignmentKey := "lab:" + labID
			if labID == "" {
				assignmentKey = "entry:" + providerID + "/" + modelID
			}

			cost := sourceModel.Cost
			inputPrice := priceValue(cost.Input)
			outputPrice := priceValue(cost.Output)
			inputHigh, outputHigh, cacheReadHigh, cacheWriteHigh, threshold := modelsDevHighPricing(cost)
			displayName := strings.TrimSpace(sourceModel.Name)
			if displayName == "" {
				displayName = modelID
			}

			entries = append(entries, ModelsDevCatalogEntry{
				Key:                 providerID + "/" + modelID,
				ProviderID:          providerID,
				ProviderName:        providerName,
				LabID:               labID,
				LabName:             resolver.labName(labID),
				AssignmentKey:       assignmentKey,
				ModelID:             modelID,
				NormalizedModelID:   normalizedID,
				DisplayName:         displayName,
				ChannelType:         channelType,
				ReleaseDate:         sourceModel.ReleaseDate,
				InputPrice:          inputPrice,
				OutputPrice:         outputPrice,
				CacheReadPrice:      priceValue(cost.CacheRead),
				CacheWritePrice:     priceValue(cost.CacheWrite),
				InputPriceHigh:      inputHigh,
				OutputPriceHigh:     outputHigh,
				CacheReadPriceHigh:  cacheReadHigh,
				CacheWritePriceHigh: cacheWriteHigh,
				HighPriceThreshold:  threshold,
				NeedsChannelType:    !matched,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ReleaseDate != entries[j].ReleaseDate {
			return entries[i].ReleaseDate > entries[j].ReleaseDate
		}
		if entries[i].ProviderName != entries[j].ProviderName {
			return entries[i].ProviderName < entries[j].ProviderName
		}
		return entries[i].ModelID < entries[j].ModelID
	})
	return entries
}

func isModelsDevTextPricingModel(modelID string, sourceModel modelsDevModel) bool {
	if sourceModel.Cost == nil || sourceModel.Cost.Input == nil || sourceModel.Cost.Output == nil {
		return false
	}
	if strings.EqualFold(sourceModel.Status, "deprecated") {
		return false
	}
	if len(sourceModel.Modalities.Output) > 0 {
		hasText := false
		for _, modality := range sourceModel.Modalities.Output {
			switch strings.ToLower(modality) {
			case "text":
				hasText = true
			case "audio", "image", "video":
				return false
			}
		}
		if !hasText {
			return false
		}
	}

	searchable := strings.ToLower(modelID + " " + sourceModel.Name)
	for _, marker := range []string{"audio", "embedding", "image", "moderation", "realtime", "transcribe", "tts", "video"} {
		if strings.Contains(searchable, marker) {
			return false
		}
	}
	return true
}

func normalizeModelsDevModelID(modelID string) string {
	afterSlash := modelID
	if i := strings.LastIndex(afterSlash, "/"); i >= 0 {
		afterSlash = afterSlash[i+1:]
	}
	if i := strings.Index(afterSlash, ":"); i >= 0 {
		afterSlash = afterSlash[:i]
	}
	afterSlash = strings.ReplaceAll(afterSlash, "@", "-")
	afterSlash = strings.TrimSpace(strings.TrimSuffix(afterSlash, "[1m]"))
	return strings.ToLower(afterSlash)
}

type modelsDevLabResolver struct {
	canonicalIDs   map[string]string
	canonicalNames map[string]string
	canonicalBases map[string]string
	knownLabs      map[string]struct{}
	labNames       map[string]string
}

func newModelsDevLabResolver(models map[string]modelsDevModel, providers map[string]modelsDevProvider) *modelsDevLabResolver {
	r := &modelsDevLabResolver{
		canonicalIDs:   make(map[string]string),
		canonicalNames: make(map[string]string),
		canonicalBases: make(map[string]string),
		knownLabs:      make(map[string]struct{}),
		labNames:       make(map[string]string),
	}
	for modelKey, sourceModel := range models {
		canonicalID := strings.TrimSpace(sourceModel.ID)
		if canonicalID == "" {
			canonicalID = modelKey
		}
		labID := modelsDevLabFromCanonicalID(canonicalID)
		if labID == "" {
			labID = modelsDevLabFromCanonicalID(modelKey)
		}
		if labID == "" {
			continue
		}
		r.knownLabs[labID] = struct{}{}
		r.canonicalIDs[strings.ToLower(canonicalID)] = labID
		r.canonicalIDs[strings.ToLower(modelKey)] = labID
		addUniqueModelsDevLab(r.canonicalBases, normalizeModelsDevModelID(canonicalID), labID)
		addUniqueModelsDevLab(r.canonicalNames, normalizeModelsDevLabLookup(sourceModel.Name), labID)
	}
	for providerKey, provider := range providers {
		providerID := strings.ToLower(strings.TrimSpace(provider.ID))
		if providerID == "" {
			providerID = strings.ToLower(strings.TrimSpace(providerKey))
		}
		if providerID == "" {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = modelsDevTitle(providerID)
		}
		r.labNames[providerID] = name
	}
	return r
}

func (r *modelsDevLabResolver) resolve(providerID, modelID, normalizedID string, sourceModel modelsDevModel) string {
	if labID := strings.ToLower(strings.TrimSpace(sourceModel.Lab)); labID != "" {
		return labID
	}
	for _, candidate := range []string{sourceModel.BaseModel, modelID, providerID + "/" + modelID} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if labID := r.canonicalIDs[candidate]; labID != "" {
			return labID
		}
		if strings.Contains(candidate, "/") {
			if labID := strings.SplitN(candidate, "/", 2)[0]; r.isKnownLab(labID) {
				return labID
			}
		}
	}
	if labID := uniqueModelsDevLab(r.canonicalBases, normalizedID); labID != "" {
		return labID
	}
	if labID := uniqueModelsDevLab(r.canonicalNames, normalizeModelsDevLabLookup(sourceModel.Name)); labID != "" {
		return labID
	}

	// 兼容旧版 api.json：没有规范模型目录时，仅对三个确定的官方模型族做保守识别。
	lower := strings.ToLower(normalizedID)
	switch {
	case strings.HasPrefix(lower, "claude-"):
		return "anthropic"
	case strings.HasPrefix(lower, "gemini-"):
		return "google"
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "chatgpt-"),
		strings.HasPrefix(lower, "codex-"), strings.HasPrefix(lower, "o1"),
		strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		return "openai"
	default:
		return ""
	}
}

func (r *modelsDevLabResolver) isKnownLab(labID string) bool {
	labID = strings.ToLower(strings.TrimSpace(labID))
	if labID == "" {
		return false
	}
	_, ok := r.knownLabs[labID]
	return ok
}

func (r *modelsDevLabResolver) labName(labID string) string {
	labID = strings.ToLower(strings.TrimSpace(labID))
	if labID == "" {
		return "未识别 Lab"
	}
	if name := r.labNames[labID]; name != "" {
		return name
	}
	switch labID {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "google":
		return "Google"
	case "xai":
		return "xAI"
	case "moonshotai":
		return "Moonshot AI"
	case "zhipuai":
		return "Zhipu AI"
	default:
		return modelsDevTitle(labID)
	}
}

func modelsDevLabFromCanonicalID(canonicalID string) string {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(canonicalID)), "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

const ambiguousModelsDevLab = "\x00"

func addUniqueModelsDevLab(index map[string]string, key, labID string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || labID == "" {
		return
	}
	if current, ok := index[key]; ok && current != labID {
		index[key] = ambiguousModelsDevLab
		return
	}
	index[key] = labID
}

func uniqueModelsDevLab(index map[string]string, key string) string {
	value := index[strings.ToLower(strings.TrimSpace(key))]
	if value == ambiguousModelsDevLab {
		return ""
	}
	return value
}

func normalizeModelsDevLabLookup(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func modelsDevTitle(value string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(r rune) bool { return r == '-' || r == '_' })
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func modelsDevChannelTypeForLab(labID string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(labID)) {
	case "anthropic":
		return util.ChannelTypeAnthropic, true
	case "openai":
		return util.ChannelTypeCodex, true
	case "google":
		return util.ChannelTypeGemini, true
	default:
		return "", false
	}
}

func modelsDevHighPricing(cost *modelsDevCost) (input, output, cacheRead, cacheWrite float64, threshold int64) {
	if cost == nil {
		return 0, 0, 0, 0, 0
	}
	var selected *modelsDevCostTier
	for i := range cost.Tiers {
		tier := &cost.Tiers[i]
		tierType := strings.ToLower(strings.TrimSpace(tier.Tier.Type))
		if (tierType != "" && tierType != "context") || tier.Tier.Size <= 0 || tier.Input == nil || tier.Output == nil {
			continue
		}
		if selected == nil || tier.Tier.Size < selected.Tier.Size {
			selected = tier
		}
	}
	if selected != nil {
		return priceValue(selected.Input), priceValue(selected.Output), priceValue(selected.CacheRead), priceValue(selected.CacheWrite), selected.Tier.Size
	}
	if cost.ContextOver200K != nil && cost.ContextOver200K.Input != nil && cost.ContextOver200K.Output != nil {
		return priceValue(cost.ContextOver200K.Input), priceValue(cost.ContextOver200K.Output),
			priceValue(cost.ContextOver200K.CacheRead), priceValue(cost.ContextOver200K.CacheWrite), 200_000
	}
	return 0, 0, 0, 0, 0
}

func priceValue(value *float64) float64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

type modelsDevImportResult struct {
	Created   int
	Updated   int
	Skipped   int
	Missing   int
	Duplicate int
}

func buildModelsDevImportEntries(catalog []ModelsDevCatalogEntry, existing []*model.ModelPricingEntry, keys []string, overwrite bool, channelTypes map[string]string) ([]*model.ModelPricingEntry, modelsDevImportResult, error) {
	byKey := make(map[string]ModelsDevCatalogEntry, len(catalog))
	for _, entry := range catalog {
		byKey[entry.Key] = entry
	}
	existingByModel := indexExistingPricing(existing)

	seenKeys := make(map[string]struct{}, len(keys))
	seenModels := make(map[string]struct{}, len(keys))
	result := modelsDevImportResult{}
	imports := make([]*model.ModelPricingEntry, 0, len(keys))
	for _, key := range keys {
		if _, ok := seenKeys[key]; ok {
			result.Duplicate++
			continue
		}
		seenKeys[key] = struct{}{}
		source, ok := byKey[key]
		if !ok {
			result.Missing++
			continue
		}
		current := existingByModel[strings.ToLower(source.NormalizedModelID)]
		if current == nil {
			current = existingByModel[strings.ToLower(source.ModelID)]
		}
		modelKey := strings.ToLower(source.NormalizedModelID)
		if current != nil {
			modelKey = strings.ToLower(current.Model)
		}
		if _, ok := seenModels[modelKey]; ok {
			result.Duplicate++
			continue
		}
		seenModels[modelKey] = struct{}{}

		if current != nil && !overwrite {
			result.Skipped++
			continue
		}

		entry := modelsDevEntryToPricing(source)
		if current != nil {
			entry.Model = current.Model
			entry.DisplayName = current.DisplayName
			if entry.DisplayName == "" {
				entry.DisplayName = source.DisplayName
			}
			entry.ChannelType = current.ChannelType
			entry.Aliases = mergePricingAliases(current.Aliases, entry.Aliases)
			if !strings.EqualFold(entry.Model, source.NormalizedModelID) {
				entry.Aliases = mergePricingAliases(entry.Aliases, []string{source.NormalizedModelID})
			}
			entry.Aliases = removePricingAlias(entry.Aliases, entry.Model)
			entry.IsDefault = current.IsDefault
			result.Updated++
		} else {
			channelType := strings.ToLower(strings.TrimSpace(source.ChannelType))
			if channelType == "" {
				channelType = strings.ToLower(strings.TrimSpace(channelTypes[source.AssignmentKey]))
			}
			if !util.IsValidChannelType(channelType) {
				label := strings.TrimSpace(source.LabName)
				if label == "" || source.LabID == "" {
					label = source.DisplayName
				}
				if label == "" {
					label = source.ModelID
				}
				return nil, result, fmt.Errorf("%s 尚未选择有效的渠道类型", label)
			}
			entry.ChannelType = channelType
			result.Created++
		}
		imports = append(imports, entry)
	}
	return imports, result, nil
}

func modelsDevEntryToPricing(source ModelsDevCatalogEntry) *model.ModelPricingEntry {
	threshold := source.HighPriceThreshold
	if source.InputPriceHigh > 0 && threshold <= 0 {
		threshold = util.DefaultHighPriceThresholdForChannel(source.ChannelType)
	} else if source.InputPriceHigh <= 0 {
		threshold = 0
	}
	aliases := make([]string, 0, 2)
	if source.ModelID != source.NormalizedModelID {
		aliases = append(aliases, source.ModelID)
		lowerRaw := strings.ToLower(source.ModelID)
		if lowerRaw != source.ModelID && lowerRaw != source.NormalizedModelID {
			aliases = append(aliases, lowerRaw)
		}
	}
	return &model.ModelPricingEntry{
		Model:               source.NormalizedModelID,
		DisplayName:         source.DisplayName,
		ChannelType:         source.ChannelType,
		InputPrice:          source.InputPrice,
		OutputPrice:         source.OutputPrice,
		InputPriceHigh:      source.InputPriceHigh,
		OutputPriceHigh:     source.OutputPriceHigh,
		HighPriceThreshold:  threshold,
		CacheReadPrice:      source.CacheReadPrice,
		CacheWritePrice:     source.CacheWritePrice,
		CacheReadPriceHigh:  source.CacheReadPriceHigh,
		CacheWritePriceHigh: source.CacheWritePriceHigh,
		Aliases:             aliases,
	}
}

func mergePricingAliases(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, alias := range group {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if _, ok := seen[alias]; ok {
				continue
			}
			seen[alias] = struct{}{}
			result = append(result, alias)
		}
	}
	return result
}

func removePricingAlias(aliases []string, modelName string) []string {
	result := aliases[:0]
	for _, alias := range aliases {
		if !strings.EqualFold(alias, modelName) {
			result = append(result, alias)
		}
	}
	return result
}

func markExistingModelsDevEntries(catalog []ModelsDevCatalogEntry, existing []*model.ModelPricingEntry) {
	existingModels := indexExistingPricing(existing)
	for i := range catalog {
		_, normalizedExists := existingModels[strings.ToLower(catalog[i].NormalizedModelID)]
		_, rawExists := existingModels[strings.ToLower(catalog[i].ModelID)]
		catalog[i].Exists = normalizedExists || rawExists
	}
}

func indexExistingPricing(existing []*model.ModelPricingEntry) map[string]*model.ModelPricingEntry {
	index := make(map[string]*model.ModelPricingEntry, len(existing))
	for _, entry := range existing {
		if entry == nil {
			continue
		}
		if key := strings.ToLower(strings.TrimSpace(entry.Model)); key != "" {
			index[key] = entry
		}
		for _, alias := range entry.Aliases {
			if key := strings.ToLower(strings.TrimSpace(alias)); key != "" {
				if _, exists := index[key]; !exists {
					index[key] = entry
				}
			}
		}
	}
	return index
}
