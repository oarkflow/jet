package main

import (
	"bytes"
	"fmt"
	"log"
	"text/template"
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

// Function to execute the template and replace placeholders with actual values
func generateSQL(params QueryParams) (string, error) {
	// Define the SQL template
	sqlTemplate := `
SELECT DISTINCT
    Event.suspend_uid,
    Event.encounter_uid,
    Event.work_item_uid,
    TO_CHAR(tbl_encounter.encounter_dos, 'MM/DD/YYYY') AS encounter_dos,
    patient_name,
    patient_fin,
    patient_mrn,
    ROUND(EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - Event.user_time_entered_utc)) / 3600 / 24, 1) AS Aging,
    tbl_facility.facility_name,
    COALESCE(provider_lov.display_name, provider_email.display_name) AS provider_lov,
    Event.suspend_reason,
    tbl_suspend_master.reason_code,
    tbl_encounter.encounter_type,
    Event.event_dos
FROM tbl_event_suspend Event
    -- Inline replacement for tbl_user_access
    JOIN (VALUES 
        {{- range $index, $value := .AccessValues }}
        {{if $index}},{{end}}({{$value.UserID}}, {{$value.WorkItemID}}, '{{$value.UserTypeID}}')
        {{- end }}
    ) AS ua(user_id, work_item_id, user_type_id)
    ON Event.work_item_uid = ua.work_item_id

    -- Inline replacement for tbl_user_type
    JOIN (VALUES 
        {{- range $index, $value := .TypeValues }}
        {{if $index}},{{end}}('{{$value.UserTypeID}}', '{{$value.UserType}}')
        {{- end }}
    ) AS ut(user_type_id, user_type)
    ON ua.user_type_id = ut.user_type_id

    -- Inline replacement for tbl_user
    JOIN (VALUES 
        {{- range $index, $value := .UserValues }}
        {{if $index}},{{end}}({{$value.UserID}}, {{$value.ProSuspendOnly}}, {{$value.TechSuspendOnly}})
        {{- end }}
    ) AS u(user_id, pro_suspend_only, tech_suspend_only)
    ON ua.user_id = u.user_id

    -- Existing joins
    JOIN tbl_encounter_detail
        ON tbl_encounter_detail.encounter_uid = Event.encounter_uid
       AND tbl_encounter_detail.work_item_uid = Event.work_item_uid
    JOIN tbl_encounter ON tbl_encounter.encounter_uid = Event.encounter_uid
    JOIN tbl_work_item ON tbl_work_item.work_item_uid = Event.work_item_uid
    JOIN tbl_facility ON tbl_facility.facility_id = tbl_work_item.facility_id
    LEFT JOIN tbl_suspend_master ON tbl_suspend_master.reason_description = Event.suspend_reason
       AND tbl_work_item.work_item_uid = tbl_suspend_master.work_item_uid
    LEFT JOIN tbl_provider provider_email ON Event.suspend_provider = provider_email.provider_email
    LEFT JOIN tbl_provider provider_lov ON Event.suspend_provider = provider_lov.provider_lov
WHERE NOT Event.suspend_released
    AND tbl_encounter.encounter_status = 'SUSPEND'
    AND ua.user_id = {{.UserID}}
    AND ut.user_type IN 
        ({{- range $index, $type := .UserTypes }}
            {{if $index}},{{end}}'{{$type}}'
        {{- end }})
    AND Event.work_item_uid = {{.WorkItemID}};
`

	// Parse the template
	tmpl, err := template.New("sql").Parse(sqlTemplate)
	if err != nil {
		return "", err
	}

	// Buffer to store the executed template
	var result bytes.Buffer

	// Execute the template with the provided parameters
	err = tmpl.Execute(&result, params)
	if err != nil {
		return "", err
	}

	// Return the rendered query
	return result.String(), nil
}

// Example usage of the function
func main() {
	// Define the data to replace the placeholders
	params := QueryParams{
		UserID:     21281,
		WorkItemID: 33,
		UserTypes:  []string{"G_SUSPEND_MGR", "SUSPEND_MANAGER"},
		AccessValues: []AccessValue{
			{UserID: 21281, WorkItemID: 29, UserTypeID: "G_SUSPEND_MGR"},
			{UserID: 21281, WorkItemID: 29, UserTypeID: "SUSPEND_MANAGER"},
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
	fmt.Println(query)
}
