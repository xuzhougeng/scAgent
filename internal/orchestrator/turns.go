package orchestrator

import (
	"fmt"
	"strings"

	"scagent/internal/models"
)

type TurnResolveResult struct {
	Strategy   models.TurnStrategy    `json:"strategy"`
	Contract   models.TurnContract    `json:"contract"`
	Answer     string                 `json:"answer,omitempty"`
	Summary    string                 `json:"summary,omitempty"`
	ResultRefs []models.TurnResultRef `json:"result_refs,omitempty"`
}

func defaultTurnContractForRequest(request PlanningRequest) models.TurnContract {
	return models.TurnContract{
		Intent:          "unresolved",
		DeliverableKind: models.TurnDeliverableUnknown,
		TargetObjectID:  targetObjectForTurnRequest(request),
		ReusePolicy:     models.TurnReusePreferExisting,
	}
}

func defaultCompletionCriteriaForDeliverable(kind models.TurnDeliverableKind) []models.TurnCompletionCriterion {
	switch kind {
	case models.TurnDeliverablePlot:
		return []models.TurnCompletionCriterion{{
			Kind:         models.TurnCompletionArtifactKind,
			ArtifactKind: models.ArtifactPlot,
		}}
	case models.TurnDeliverableFile:
		return []models.TurnCompletionCriterion{{
			Kind:         models.TurnCompletionArtifactKind,
			ArtifactKind: models.ArtifactFile,
		}}
	case models.TurnDeliverableTable:
		return []models.TurnCompletionCriterion{{
			Kind:         models.TurnCompletionArtifactKind,
			ArtifactKind: models.ArtifactTable,
		}}
	case models.TurnDeliverableObject:
		return []models.TurnCompletionCriterion{{
			Kind: models.TurnCompletionObjectID,
		}}
	case models.TurnDeliverableText:
		return []models.TurnCompletionCriterion{{
			Kind: models.TurnCompletionTextAnswer,
		}}
	default:
		return []models.TurnCompletionCriterion{{
			Kind: models.TurnCompletionAnyResult,
		}}
	}
}

func targetObjectForTurnRequest(request PlanningRequest) string {
	switch {
	case request.FocusObject != nil && request.FocusObject.ID != "":
		return request.FocusObject.ID
	case request.GlobalObject != nil && request.GlobalObject.ID != "":
		return request.GlobalObject.ID
	default:
		return ""
	}
}

func normalizeTurnResolveResult(request PlanningRequest, result *TurnResolveResult) *TurnResolveResult {
	if result == nil {
		result = &TurnResolveResult{}
	}
	if result.Contract.DeliverableKind == "" {
		result.Contract = defaultTurnContractForRequest(request)
	}
	if result.Contract.TargetObjectID == "" {
		result.Contract.TargetObjectID = targetObjectForTurnRequest(request)
	}
	if result.Contract.ReusePolicy == "" {
		result.Contract.ReusePolicy = models.TurnReusePreferExisting
	}
	if result.Contract.DeliverableKind != models.TurnDeliverableUnknown && len(result.Contract.CompletionCriteria) == 0 {
		result.Contract.CompletionCriteria = defaultCompletionCriteriaForDeliverable(result.Contract.DeliverableKind)
	}
	if result.Strategy == "" {
		result.Strategy = models.TurnStrategyExecute
	}
	if result.Strategy == models.TurnStrategyAnswerText && strings.TrimSpace(result.Answer) == "" {
		result.Strategy = models.TurnStrategyExecute
	}
	return result
}

func buildFallbackTurnResolveResult(request PlanningRequest) *TurnResolveResult {
	contract := defaultTurnContractForRequest(request)
	return &TurnResolveResult{
		Strategy: models.TurnStrategyExecute,
		Contract: contract,
		Summary:  "Turn resolver 未返回可用结构，保守地转入执行路径。",
	}
}

func cloneTurnForPlanning(in *models.Turn) *models.Turn {
	if in == nil {
		return nil
	}
	out := *in
	out.Contract = cloneTurnContractForPlanning(in.Contract)
	if len(in.ResultRefs) > 0 {
		out.ResultRefs = append([]models.TurnResultRef(nil), in.ResultRefs...)
	}
	return &out
}

func cloneTurnContractForPlanning(in models.TurnContract) models.TurnContract {
	out := in
	if len(in.CompletionCriteria) > 0 {
		out.CompletionCriteria = append([]models.TurnCompletionCriterion(nil), in.CompletionCriteria...)
	}
	return out
}

func deriveTurnResultRefsFromSnapshot(turn *models.Turn, job *models.Job, snapshot *models.SessionSnapshot) []models.TurnResultRef {
	if turn == nil || job == nil {
		return nil
	}

	artifactByID := make(map[string]*models.Artifact)
	objectByID := make(map[string]*models.ObjectMeta)
	if snapshot != nil {
		for _, artifact := range snapshot.Artifacts {
			if artifact == nil {
				continue
			}
			artifactByID[artifact.ID] = artifact
		}
		for _, object := range snapshot.Objects {
			if object == nil {
				continue
			}
			objectByID[object.ID] = object
		}
	}

	artifactIDs := make([]string, 0)
	objectIDs := make([]string, 0)
	for stepIndex := len(job.Steps) - 1; stepIndex >= 0; stepIndex-- {
		step := job.Steps[stepIndex]
		if step.Status != models.JobSucceeded {
			continue
		}
		for artifactIndex := len(step.ArtifactIDs) - 1; artifactIndex >= 0; artifactIndex-- {
			artifactIDs = append(artifactIDs, step.ArtifactIDs[artifactIndex])
		}
		if step.OutputObjectID != "" {
			objectIDs = append(objectIDs, step.OutputObjectID)
		}
	}

	artifactIDs = uniqueStringsPreserveOrder(artifactIDs)
	objectIDs = uniqueStringsPreserveOrder(objectIDs)

	refs := make([]models.TurnResultRef, 0, len(artifactIDs)+len(objectIDs))
	switch turn.Contract.DeliverableKind {
	case models.TurnDeliverablePlot:
		for _, criterion := range turn.Contract.CompletionCriteria {
			if criterion.Kind == models.TurnCompletionArtifactID && criterion.ArtifactID != "" {
				ref := models.TurnResultRef{
					Kind:       models.TurnResultArtifact,
					ArtifactID: criterion.ArtifactID,
				}
				if artifact, ok := artifactByID[criterion.ArtifactID]; ok {
					ref.ArtifactKind = artifact.Kind
				}
				refs = append(refs, ref)
			}
		}
		for _, artifactID := range artifactIDs {
			ref := models.TurnResultRef{
				Kind:       models.TurnResultArtifact,
				ArtifactID: artifactID,
			}
			if artifact, ok := artifactByID[artifactID]; ok {
				ref.ArtifactKind = artifact.Kind
			}
			refs = append(refs, ref)
		}
	case models.TurnDeliverableFile, models.TurnDeliverableTable:
		for _, artifactID := range artifactIDs {
			ref := models.TurnResultRef{
				Kind:       models.TurnResultArtifact,
				ArtifactID: artifactID,
			}
			if artifact, ok := artifactByID[artifactID]; ok {
				ref.ArtifactKind = artifact.Kind
			}
			refs = append(refs, ref)
		}
	case models.TurnDeliverableObject:
		for _, objectID := range objectIDs {
			if _, ok := objectByID[objectID]; ok || objectID != "" {
				refs = append(refs, models.TurnResultRef{Kind: models.TurnResultObject, ObjectID: objectID})
			}
		}
	default:
		for _, artifactID := range artifactIDs {
			ref := models.TurnResultRef{
				Kind:       models.TurnResultArtifact,
				ArtifactID: artifactID,
			}
			if artifact, ok := artifactByID[artifactID]; ok {
				ref.ArtifactKind = artifact.Kind
			}
			refs = append(refs, ref)
		}
		for _, objectID := range objectIDs {
			if _, ok := objectByID[objectID]; ok || objectID != "" {
				refs = append(refs, models.TurnResultRef{Kind: models.TurnResultObject, ObjectID: objectID})
			}
		}
	}
	return uniqueTurnResultRefs(refs)
}

func evaluateTurnCompletion(turn *models.Turn, job *models.Job) *CompletionEvaluation {
	if turn == nil {
		return nil
	}
	if turn.Contract.DeliverableKind == models.TurnDeliverableUnknown && len(turn.Contract.CompletionCriteria) == 0 {
		return nil
	}
	switch turn.Strategy {
	case models.TurnStrategyAnswerText:
		return &CompletionEvaluation{
			Completed: true,
			Reason:    "本轮请求是文本交付，当前回答已完成请求。",
		}
	case models.TurnStrategyReuseExistingArtifact:
		if turnMeetsCompletionCriteria(turn, turn.ResultRefs) {
			return &CompletionEvaluation{
				Completed: true,
				Reason:    "已复用当前会话中满足请求的既有结果。",
			}
		}
		return &CompletionEvaluation{
			Completed: false,
			Reason:    "本轮计划复用既有结果，但当前还没有绑定到满足请求的结果引用。",
		}
	}

	if job == nil {
		return nil
	}
	resultRefs := uniqueTurnResultRefs(turn.ResultRefs)
	if len(resultRefs) == 0 {
		resultRefs = deriveTurnResultRefsFromSnapshot(turn, job, nil)
	}
	if turnMeetsCompletionCriteria(turn, resultRefs) {
		switch turn.Contract.DeliverableKind {
		case models.TurnDeliverablePlot:
			return &CompletionEvaluation{Completed: true, Reason: "目标图像已生成，当前请求已完成。"}
		case models.TurnDeliverableFile:
			return &CompletionEvaluation{Completed: true, Reason: "导出文件已生成，当前请求已完成。"}
		case models.TurnDeliverableTable:
			return &CompletionEvaluation{Completed: true, Reason: "目标表格已生成，当前请求已完成。"}
		case models.TurnDeliverableObject:
			return &CompletionEvaluation{Completed: true, Reason: "目标对象结果已生成，当前请求已完成。"}
		default:
			return &CompletionEvaluation{Completed: true, Reason: "本轮请求所需结果已生成。"}
		}
	}
	return &CompletionEvaluation{
		Completed: false,
		Reason:    "当前还没有拿到满足本轮交付条件的结果。",
	}
}

func turnMeetsCompletionCriteria(turn *models.Turn, resultRefs []models.TurnResultRef) bool {
	if turn == nil {
		return false
	}
	criteria := turn.Contract.CompletionCriteria
	if len(criteria) == 0 {
		return len(resultRefs) > 0
	}

	for _, criterion := range criteria {
		switch criterion.Kind {
		case models.TurnCompletionAnyResult:
			if len(resultRefs) == 0 {
				return false
			}
		case models.TurnCompletionTextAnswer:
			if len(resultRefs) == 0 {
				return false
			}
			found := false
			for _, ref := range resultRefs {
				if ref.Kind == models.TurnResultText && strings.TrimSpace(ref.Text) != "" {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case models.TurnCompletionArtifactID:
			if !hasArtifactRef(resultRefs, criterion.ArtifactID) {
				return false
			}
		case models.TurnCompletionObjectID:
			if criterion.ObjectID != "" && !hasObjectRef(resultRefs, criterion.ObjectID) {
				return false
			}
			if criterion.ObjectID == "" && !hasAnyObjectRef(resultRefs) {
				return false
			}
		case models.TurnCompletionArtifactKind:
			if !hasArtifactKindRef(resultRefs, criterion.ArtifactKind) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func hasArtifactRef(resultRefs []models.TurnResultRef, artifactID string) bool {
	for _, ref := range resultRefs {
		if ref.Kind == models.TurnResultArtifact && ref.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

func hasObjectRef(resultRefs []models.TurnResultRef, objectID string) bool {
	for _, ref := range resultRefs {
		if ref.Kind == models.TurnResultObject && ref.ObjectID == objectID {
			return true
		}
	}
	return false
}

func hasAnyObjectRef(resultRefs []models.TurnResultRef) bool {
	for _, ref := range resultRefs {
		if ref.Kind == models.TurnResultObject && ref.ObjectID != "" {
			return true
		}
	}
	return false
}

func hasArtifactKindRef(resultRefs []models.TurnResultRef, kind models.ArtifactKind) bool {
	for _, ref := range resultRefs {
		if ref.Kind == models.TurnResultArtifact && ref.ArtifactID != "" && ref.ArtifactKind == kind {
			return true
		}
	}
	return false
}

func uniqueTurnResultRefs(values []models.TurnResultRef) []models.TurnResultRef {
	out := make([]models.TurnResultRef, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := string(value.Kind) + ":" + value.ArtifactID + ":" + value.ObjectID + ":" + value.Text
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueStringsPreserveOrder(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func selectTurnResultRefs(snapshot *models.SessionSnapshot, refs []models.TurnResultRef) []models.TurnResultRef {
	if snapshot == nil || len(refs) == 0 {
		return nil
	}
	artifactIDs := make(map[string]struct{}, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		if artifact != nil {
			artifactIDs[artifact.ID] = struct{}{}
		}
	}
	objectIDs := make(map[string]struct{}, len(snapshot.Objects))
	for _, object := range snapshot.Objects {
		if object != nil {
			objectIDs[object.ID] = struct{}{}
		}
	}

	selected := make([]models.TurnResultRef, 0, len(refs))
	for _, ref := range refs {
		switch ref.Kind {
		case models.TurnResultArtifact:
			for _, artifact := range snapshot.Artifacts {
				if artifact == nil || artifact.ID != ref.ArtifactID {
					continue
				}
				enriched := ref
				enriched.ArtifactKind = artifact.Kind
				selected = append(selected, enriched)
				break
			}
		case models.TurnResultObject:
			if _, ok := objectIDs[ref.ObjectID]; ok {
				selected = append(selected, ref)
			}
		case models.TurnResultText:
			if strings.TrimSpace(ref.Text) != "" {
				selected = append(selected, ref)
			}
		}
	}
	return uniqueTurnResultRefs(selected)
}

func formatTurnResolutionContext(request PlanningRequest) []string {
	lines := []string{
		"- current_turn=" + formatTurnContextValue(request.CurrentTurn),
	}
	if len(request.RecentTurns) == 0 {
		return append(lines, "- recent_turns=none")
	}
	lines = append(lines, "- recent_turns:")
	for _, turn := range request.RecentTurns {
		lines = append(lines, "  "+formatTurnContextValue(turn))
	}
	return lines
}

func formatTurnEvaluationContext(currentTurn *models.Turn, recentTurns []*models.Turn) []string {
	lines := []string{
		"- current_turn=" + formatTurnContextValue(currentTurn),
	}
	if len(recentTurns) == 0 {
		return append(lines, "- recent_turns=none")
	}
	lines = append(lines, "- recent_turns:")
	for _, turn := range recentTurns {
		lines = append(lines, "  "+formatTurnContextValue(turn))
	}
	return lines
}

func formatPlannerTurnContext(currentTurn *models.Turn, recentTurns []*models.Turn, limit int) []string {
	lines := []string{
		"- current_turn=" + formatTurnContextValue(currentTurn),
	}
	if len(recentTurns) == 0 || limit <= 0 {
		return append(lines, "- recent_turns=none")
	}

	filtered := make([]*models.Turn, 0, len(recentTurns))
	for _, turn := range recentTurns {
		if turn != nil {
			filtered = append(filtered, turn)
		}
	}
	if len(filtered) == 0 {
		return append(lines, "- recent_turns=none")
	}

	start := len(filtered) - limit
	if start < 0 {
		start = 0
	}
	lines = append(lines, "- recent_turns:")
	for _, turn := range filtered[start:] {
		lines = append(lines, "  "+formatTurnContextValue(turn))
	}
	return lines
}

func formatTurnContextValue(turn *models.Turn) string {
	if turn == nil {
		return "none"
	}
	resultRefs := make([]string, 0, len(turn.ResultRefs))
	for _, ref := range turn.ResultRefs {
		switch ref.Kind {
		case models.TurnResultArtifact:
			artifactKind := string(ref.ArtifactKind)
			if artifactKind == "" {
				artifactKind = "unknown"
			}
			resultRefs = append(resultRefs, "artifact:"+artifactKind+":"+ref.ArtifactID)
		case models.TurnResultObject:
			resultRefs = append(resultRefs, "object:"+ref.ObjectID)
		case models.TurnResultText:
			resultRefs = append(resultRefs, "text:"+truncateText(ref.Text, 60))
		}
	}
	return fmt.Sprintf(
		"id=%s | status=%s | strategy=%s | deliverable=%s | target=%s | summary=%s | result_refs=%s",
		turn.ID,
		turn.Status,
		turn.Strategy,
		turn.Contract.DeliverableKind,
		turn.Contract.TargetObjectID,
		truncateText(turn.Summary, 120),
		strings.Join(resultRefs, ","),
	)
}

func defaultReuseTurnAnswer(_ []models.TurnResultRef, _ string) string {
	return "在这儿：我把已有结果挂上来了。"
}
