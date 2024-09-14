package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/oarkflow/jet"
)

var reSpace = regexp.MustCompile(`\s+`)

func removeExtraWhitespace(input string) string {
	return strings.TrimSpace(reSpace.ReplaceAllString(input, " "))
}

func generateSQL(params map[string]any) (string, error) {
	jet.DefaultSet(jet.WithDelims("{{", "}}"))
	const sqlTemplate = `
SELECT DISTINCT
    Event.suspend_event_id,
    Event.encounter_id,
    Event.work_item_id,
    TO_CHAR(encounters.encounter_dos, 'MM/DD/YYYY') AS encounter_dos,
    patient_name,
    patient_fin,
    patient_mrn,
    ROUND(EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - Event.user_time_entered)) / 3600 / 24, 1) AS Aging,
    facilities.facility_name,
    COALESCE(provider_lov.display_name, provider_email.display_name) AS provider_lov,
    Event.suspend_reason,
    suspend_master.reason_code,
    encounters.encounter_type,
    Event.event_dos
FROM suspend_events Event
    JOIN (VALUES
        {{ range index, value := access_values }}
        {{if index}},{{end}}({{value.user_id}}, {{value.work_item_id}}, '{{value.user_type_id}}')
        {{ end }}
    ) AS ua(user_id, work_item_id, user_type_id)
    ON Event.work_item_id = ua.work_item_id

    JOIN (VALUES
        {{ range index, value := type_values }}
        {{if index}},{{end}}('{{value.user_type_id}}', '{{value.user_type}}')
        {{ end }}
    ) AS ut(user_type_id, user_type)
    ON ua.user_type_id = ut.user_type_id

    JOIN (VALUES
        {{ range index, value := user_values }}
        {{if index}},{{end}}({{value.user_id}}, {{value.pro_suspend_only}}, {{value.TechSuspendOnly}})
        {{ end }}
    ) AS u(user_id, pro_suspend_only, tech_suspend_only)
    ON ua.user_id = u.user_id

    JOIN encounter_details
        ON encounter_details.encounter_id = Event.encounter_id
       AND encounter_details.work_item_id = Event.work_item_id
    JOIN encounters ON encounters.encounter_id = Event.encounter_id
    JOIN work_items ON work_items.work_item_id = Event.work_item_id
    JOIN facilities ON facilities.facility_id = work_items.facility_id
    LEFT JOIN suspend_master ON suspend_master.reason_description = Event.suspend_reason
       AND work_items.work_item_id = suspend_master.work_item_id
    LEFT JOIN providers provider_email ON Event.suspend_provider = provider_email.provider_email
    LEFT JOIN providers provider_lov ON Event.suspend_provider = provider_lov.provider_lov
WHERE NOT Event.suspend_released
    AND encounter_details.encounter_status = 'SUSPEND'
    AND ua.user_id = {{user_id}}
    AND ut.user_type IN
        ({{ range index, type := user_types }}
            {{if index}},{{end}}'{{type}}'
        {{ end }})
    AND Event.work_item_id = {{work_item_id}}
    {{ if user_values }}
        {{ range index, value := user_values }}
            {{ if value.pro_suspend_only }}
                {{ if value.TechSuspendOnly }}
                    AND (u.pro_suspend_only = true AND u.tech_suspend_only = true AND work_items.work_item_type_id NOT IN (1, 2))
                {{ else }}
                    AND (u.pro_suspend_only = true AND u.tech_suspend_only = false AND work_items.work_item_type_id <> 2)
                {{ end }}
            {{ else }}
                {{ if value.TechSuspendOnly }}
                    AND (u.pro_suspend_only = false AND u.tech_suspend_only = true AND work_items.work_item_type_id <> 1)
                {{ else }}
                    AND (u.pro_suspend_only = false AND u.tech_suspend_only = false)
                {{ end }}
            {{ end }}
        {{ end }}
    {{ end }}
;`

	return jet.Parse(sqlTemplate, params)
}

func main() {
	params := map[string]any{
		"user_id":      21281,
		"work_item_id": 33,
		"user_types":   []string{"G_SUSPEND_MGR", "SUSPEND_MANAGER"},
		"access_values": []map[string]any{
			{"user_id": 21281, "work_item_id": 33, "user_type_id": "G_SUSPEND_MGR"},
			{"user_id": 21281, "work_item_id": 33, "user_type_id": "SUSPEND_MANAGER"},
		},
		"type_values": []map[string]any{
			{"user_type_id": "SUSPEND_MANAGER", "user_type": "SUSPEND_MANAGER"},
			{"user_type_id": "G_SUSPEND_MGR", "user_type": "G_SUSPEND_MGR"},
		},
		"user_values": []map[string]any{
			{"user_id": 21281, "pro_suspend_only": true, "TechSuspendOnly": false},
		},
	}
	query, err := generateSQL(params)
	if err != nil {
		log.Fatalf("Error generating SQL: %v", err)
	}
	fmt.Println(removeExtraWhitespace(query))
}
