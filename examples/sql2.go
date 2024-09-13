package main

import (
	"fmt"
	"github.com/oarkflow/jet"
	"log"
	"regexp"
	"strings"
)

// Define a struct to hold the template data
type QueryParams struct {
	UserID       int
	WorkItemID   int
	UserTypes    []string
	AccessValues []AccessValue
	TypeValues   []UserTypeValue
	UserValues   []UserValue
}

// Define structs for each of the inline value sets
type AccessValue struct {
	UserID     int
	WorkItemID int
	UserTypeID string
}

type UserTypeValue struct {
	UserTypeID string
	UserType   string
}

type UserValue struct {
	UserID          int
	ProSuspendOnly  bool
	TechSuspendOnly bool
}

var reSpace = regexp.MustCompile(`\s+`)

// removeExtraWhitespace removes extra whitespace and new lines from a string
func removeExtraWhitespace(input string) string {
	return strings.TrimSpace(reSpace.ReplaceAllString(input, " "))
}

// Function to generate the SQL query using Jet template
func generateSQL(params QueryParams) (string, error) {
	jet.DefaultSet(jet.WithDelims("{{", "}}"))
	// Define the SQL template
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
        {{ range index, value := AccessValues }}
        {{if index}},{{end}}({{value.UserID}}, {{value.WorkItemID}}, '{{value.UserTypeID}}')
        {{ end }}
    ) AS ua(user_id, work_item_id, user_type_id)
    ON Event.work_item_id = ua.work_item_id

    JOIN (VALUES 
        {{ range index, value := TypeValues }}
        {{if index}},{{end}}('{{value.UserTypeID}}', '{{value.UserType}}')
        {{ end }}
    ) AS ut(user_type_id, user_type)
    ON ua.user_type_id = ut.user_type_id

    JOIN (VALUES 
        {{ range index, value := UserValues }}
        {{if index}},{{end}}({{value.UserID}}, {{value.ProSuspendOnly}}, {{value.TechSuspendOnly}})
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
    AND ua.user_id = {{UserID}}
    AND ut.user_type IN 
        ({{ range index, type := UserTypes }}
            {{if index}},{{end}}'{{type}}'
        {{ end }})
    AND Event.work_item_id = {{WorkItemID}}
    {{ if UserValues }}
        {{ range index, value := UserValues }}
            {{ if value.ProSuspendOnly }}
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

// Example usage of the function
func main() {
	// Define the data to replace the placeholders
	params := QueryParams{
		UserID:     21281,
		WorkItemID: 33,
		UserTypes:  []string{"G_SUSPEND_MGR", "SUSPEND_MANAGER"},
		AccessValues: []AccessValue{
			{UserID: 21281, WorkItemID: 33, UserTypeID: "G_SUSPEND_MGR"},
			{UserID: 21281, WorkItemID: 33, UserTypeID: "SUSPEND_MANAGER"},
		},
		TypeValues: []UserTypeValue{
			{UserTypeID: "SUSPEND_MANAGER", UserType: "SUSPEND_MANAGER"},
			{UserTypeID: "G_SUSPEND_MGR", UserType: "G_SUSPEND_MGR"},
		},
		UserValues: []UserValue{
			{UserID: 21281, ProSuspendOnly: true, TechSuspendOnly: false},
		},
	}

	// Generate the SQL query
	query, err := generateSQL(params)
	if err != nil {
		log.Fatalf("Error generating SQL: %v", err)
	}

	// Print the generated query
	fmt.Println(removeExtraWhitespace(query))
}
