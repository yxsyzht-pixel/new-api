package authz

const (
	ResourceToken = "token"

	// ActionManageAll covers reading and changing keys that belong to someone
	// else. A user always has full control of their own keys without it; this
	// is only about reaching across accounts.
	ActionManageAll = "manage_all"

	// ActionStaffIDFreeform is writing a staff number the company directory
	// does not know. Everyone else picks from the directory, which is what
	// keeps a transcript attributable to a real person.
	ActionStaffIDFreeform = "staff_id_freeform"
)

var (
	TokenManageAll       = Permission{Resource: ResourceToken, Action: ActionManageAll}
	TokenStaffIDFreeform = Permission{Resource: ResourceToken, Action: ActionStaffIDFreeform}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceToken,
		LabelKey: "API Key Management",
		Actions: []ActionDefinition{
			{
				Action:         ActionManageAll,
				LabelKey:       "Manage everyone’s API keys",
				DescriptionKey: "List, create, edit, and delete API keys belonging to any user. Own keys need no permission.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionStaffIDFreeform,
				LabelKey:       "Type a staff ID freehand",
				DescriptionKey: "Enter a staff number the company directory does not list. Without it, the staff number must be chosen from the directory — which is what keeps a transcript attributable to a real person.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
