package repository

import (
	"fmt"
	"strings"
)

// Match the user-facing media classification, including token-billed images and
// historical refunds with no output. Video takes precedence over image metadata.
// Classify before pagination so both the count and each page use the same filter.
const usageLogTypeExpression = `(CASE
 WHEN COALESCE(music_task_id, '') <> '' OR request_id LIKE 'music:%' THEN 'audio'
 WHEN billing_mode = 'video_duration' OR request_type = 5 OR COALESCE(video_count, 0) > 0 OR request_id LIKE 'video:%' THEN 'video'
 WHEN COALESCE(image_task_id, '') <> '' OR billing_mode = 'image' OR COALESCE(image_count, 0) > 0 OR COALESCE(image_output_tokens, 0) > 0 OR COALESCE(image_output_cost, 0) > 0 OR request_id LIKE 'image:%' THEN 'image'
 ELSE 'text' END)`

func appendUsageLogTypeWhereCondition(conditions []string, args []any, usageType string) ([]string, []any) {
	if usageType == "" {
		return conditions, args
	}
	conditions = append(conditions, fmt.Sprintf("%s = $%d", usageLogTypeExpression, len(args)+1))
	return conditions, append(args, usageType)
}

func appendUsageLogTypeQueryFilter(query string, args []any, usageType, alias string) (string, []any) {
	conditions, args := appendUsageLogTypeWhereCondition(nil, args, usageType)
	if len(conditions) == 0 {
		return query, args
	}
	condition := conditions[0]
	if alias != "" {
		for _, column := range []string{"billing_mode", "request_type", "video_count", "request_id", "music_task_id", "image_task_id", "image_count", "image_output_tokens", "image_output_cost"} {
			condition = strings.ReplaceAll(condition, column, alias+"."+column)
		}
	}
	return query + " AND " + condition, args
}
