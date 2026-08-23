package authz

const (
	ResourceToken = "token"

	// ActionManageAll covers reading and changing keys that belong to someone
	// else. A user always has full control of their own keys without it; this
	// is only about reaching across accounts.
	ActionManageAll = "manage_all"
)

var TokenManageAll = Permission{Resource: ResourceToken, Action: ActionManageAll}

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
		},
	})
}
