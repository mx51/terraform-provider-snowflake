package resources

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Snowflake-Labs/terraform-provider-snowflake/pkg/helpers"
	"github.com/Snowflake-Labs/terraform-provider-snowflake/pkg/internal/collections"
	"github.com/Snowflake-Labs/terraform-provider-snowflake/pkg/schemas"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"

	"github.com/Snowflake-Labs/terraform-provider-snowflake/pkg/internal/provider"
	"github.com/Snowflake-Labs/terraform-provider-snowflake/pkg/sdk"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

var networkPolicySchema = map[string]*schema.Schema{
	"name": {
		Type:        schema.TypeString,
		Required:    true,
		Description: "Specifies the identifier for the network policy; must be unique for the account in which the network policy is created.",
	},
	"allowed_network_rule_list": {
		Type: schema.TypeSet,
		Elem: &schema.Schema{
			Type:             schema.TypeString,
			ValidateDiagFunc: IsValidIdentifier[sdk.SchemaObjectIdentifier](),
		},
		DiffSuppressFunc: NormalizeAndCompareIdentifiersInSet("allowed_network_rule_list"),
		Optional:         true,
		Description:      "Specifies a list of fully qualified network rules that contain the network identifiers that are allowed access to Snowflake.",
	},
	"blocked_network_rule_list": {
		Type: schema.TypeSet,
		Elem: &schema.Schema{
			Type:             schema.TypeString,
			ValidateDiagFunc: IsValidIdentifier[sdk.SchemaObjectIdentifier](),
		},
		DiffSuppressFunc: NormalizeAndCompareIdentifiersInSet("blocked_network_rule_list"),
		Optional:         true,
		Description:      "Specifies a list of fully qualified network rules that contain the network identifiers that are denied access to Snowflake.",
	},
	"allowed_ip_list": {
		Type:        schema.TypeSet,
		Elem:        &schema.Schema{Type: schema.TypeString},
		Optional:    true,
		Description: "Specifies one or more IPv4 addresses (CIDR notation) that are allowed access to your Snowflake account.",
	},
	"blocked_ip_list": {
		Type: schema.TypeSet,
		Elem: &schema.Schema{
			Type:             schema.TypeString,
			ValidateDiagFunc: isNotEqualTo("0.0.0.0/0", "**Do not** add `0.0.0.0/0` to `blocked_ip_list`, in order to block all IP addresses except a select list, you only need to add IP addresses to `allowed_ip_list`."),
		},
		Optional:    true,
		Description: "Specifies one or more IPv4 addresses (CIDR notation) that are denied access to your Snowflake account. **Do not** add `0.0.0.0/0` to `blocked_ip_list`, in order to block all IP addresses except a select list, you only need to add IP addresses to `allowed_ip_list`.",
	},
	"comment": {
		Type:        schema.TypeString,
		Optional:    true,
		Description: "Specifies a comment for the network policy.",
	},
	ShowOutputAttributeName: {
		Type:        schema.TypeList,
		Computed:    true,
		Description: "Outputs the result of `SHOW NETWORK POLICIES` for the given network policy.",
		Elem: &schema.Resource{
			Schema: schemas.ShowNetworkPolicySchema,
		},
	},
	DescribeOutputAttributeName: {
		Type:        schema.TypeList,
		Computed:    true,
		Description: "Outputs the result of `DESCRIBE NETWORK POLICY` for the given network policy.",
		Elem: &schema.Resource{
			Schema: schemas.DescribeNetworkPolicySchema,
		},
	},
}

func NetworkPolicy() *schema.Resource {
	return &schema.Resource{
		Schema: networkPolicySchema,

		CreateContext: CreateContextNetworkPolicy,
		ReadContext:   ReadContextNetworkPolicy,
		UpdateContext: UpdateContextNetworkPolicy,
		DeleteContext: DeleteContextNetworkPolicy,
		Description:   "Resource used to control network traffic. For more information, check an [official guide](https://docs.snowflake.com/en/user-guide/network-policies) on controlling network traffic with network policies.",

		CustomizeDiff: customdiff.All(
			// For now, allowed_network_rule_list and blocked_network_rule_list have to stay commented and the implementation
			// for ComputedIfAnyAttributeChanged has to be adjusted. The main issue lays in fields that have diff suppression.
			// When the value in state and the value in config are different (which is normal with diff suppressions) show
			// and describe outputs are constantly recomputed (which will appear in every terraform plan).
			ComputedIfAnyAttributeChanged(
				ShowOutputAttributeName,
				// "allowed_network_rule_list",
				// "blocked_network_rule_list",
				"allowed_ip_list",
				"blocked_ip_list",
				"comment",
			),
			ComputedIfAnyAttributeChanged(
				DescribeOutputAttributeName,
				// "allowed_network_rule_list",
				// "blocked_network_rule_list",
				"allowed_ip_list",
				"blocked_ip_list",
			),
		),

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
	}
}

func CreateContextNetworkPolicy(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	id := sdk.NewAccountObjectIdentifier(d.Get("name").(string))
	req := sdk.NewCreateNetworkPolicyRequest(id)

	if v, ok := d.GetOk("comment"); ok {
		req.WithComment(v.(string))
	}

	if v, ok := d.GetOk("allowed_network_rule_list"); ok {
		req.WithAllowedNetworkRuleList(parseNetworkRulesList(v))
	}

	if v, ok := d.GetOk("blocked_network_rule_list"); ok {
		req.WithBlockedNetworkRuleList(parseNetworkRulesList(v))
	}

	if v, ok := d.GetOk("allowed_ip_list"); ok {
		req.WithAllowedIpList(parseIPList(v))
	}

	if v, ok := d.GetOk("blocked_ip_list"); ok {
		req.WithBlockedIpList(parseIPList(v))
	}

	client := meta.(*provider.Context).Client
	err := client.NetworkPolicies.Create(ctx, req)
	if err != nil {
		return diag.Diagnostics{
			diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Error creating network policy",
				Detail:   fmt.Sprintf("error creating network policy %v err = %v", id.Name(), err),
			},
		}
	}

	d.SetId(helpers.EncodeSnowflakeID(id))

	return ReadContextNetworkPolicy(ctx, d, meta)
}

func ReadContextNetworkPolicy(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	client := meta.(*provider.Context).Client
	id := helpers.DecodeSnowflakeID(d.Id()).(sdk.AccountObjectIdentifier)

	networkPolicy, err := client.NetworkPolicies.ShowByID(ctx, id)
	if err != nil {
		if errors.Is(err, sdk.ErrObjectNotFound) {
			d.SetId("")
			return diag.Diagnostics{
				diag.Diagnostic{
					Severity: diag.Warning,
					Summary:  "Failed to retrieve network policy. Target object not found. Marking the resource as removed.",
					Detail:   fmt.Sprintf("Id: %s", d.Id()),
				},
			}
		}
		return diag.Diagnostics{
			diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to retrieve network policy",
				Detail:   fmt.Sprintf("Id: %s\nError: %s", id.Name(), err),
			},
		}
	}

	if err = d.Set("name", sdk.NewAccountObjectIdentifier(networkPolicy.Name).Name()); err != nil {
		return diag.FromErr(err)
	}

	if err = d.Set("comment", networkPolicy.Comment); err != nil {
		return diag.FromErr(err)
	}

	policyProperties, err := client.NetworkPolicies.Describe(ctx, id)
	if err != nil {
		return diag.FromErr(err)
	}

	allowedIpList := make([]string, 0)
	if allowedIpListProperty, err := collections.FindOne(policyProperties, func(prop sdk.NetworkPolicyProperty) bool { return prop.Name == "ALLOWED_IP_LIST" }); err == nil {
		allowedIpList = append(allowedIpList, sdk.ParseCommaSeparatedStringArray(allowedIpListProperty.Value, false)...)
	}
	if err = d.Set("allowed_ip_list", allowedIpList); err != nil {
		return diag.FromErr(err)
	}

	blockedIpList := make([]string, 0)
	if blockedIpListProperty, err := collections.FindOne(policyProperties, func(prop sdk.NetworkPolicyProperty) bool { return prop.Name == "BLOCKED_IP_LIST" }); err == nil {
		blockedIpList = append(blockedIpList, sdk.ParseCommaSeparatedStringArray(blockedIpListProperty.Value, false)...)
	}
	if err = d.Set("blocked_ip_list", blockedIpList); err != nil {
		return diag.FromErr(err)
	}

	allowedNetworkRules := make([]string, 0)
	if allowedNetworkRuleList, err := collections.FindOne(policyProperties, func(prop sdk.NetworkPolicyProperty) bool { return prop.Name == "ALLOWED_NETWORK_RULE_LIST" }); err == nil {
		networkRules, err := sdk.ParseNetworkRulesSnowflakeDto(allowedNetworkRuleList.Value)
		if err != nil {
			return diag.FromErr(err)
		}
		for _, networkRule := range networkRules {
			allowedNetworkRules = append(allowedNetworkRules, sdk.NewSchemaObjectIdentifierFromFullyQualifiedName(networkRule.FullyQualifiedRuleName).FullyQualifiedName())
		}
	}
	if err = d.Set("allowed_network_rule_list", allowedNetworkRules); err != nil {
		return diag.FromErr(err)
	}

	blockedNetworkRules := make([]string, 0)
	if blockedNetworkRuleList, err := collections.FindOne(policyProperties, func(prop sdk.NetworkPolicyProperty) bool { return prop.Name == "BLOCKED_NETWORK_RULE_LIST" }); err == nil {
		networkRules, err := sdk.ParseNetworkRulesSnowflakeDto(blockedNetworkRuleList.Value)
		if err != nil {
			return diag.FromErr(err)
		}
		for _, networkRule := range networkRules {
			blockedNetworkRules = append(blockedNetworkRules, sdk.NewSchemaObjectIdentifierFromFullyQualifiedName(networkRule.FullyQualifiedRuleName).FullyQualifiedName())
		}
	}
	if err = d.Set("blocked_network_rule_list", blockedNetworkRules); err != nil {
		return diag.FromErr(err)
	}

	if err = d.Set(ShowOutputAttributeName, []map[string]any{schemas.NetworkPolicyToSchema(networkPolicy)}); err != nil {
		return diag.FromErr(err)
	}

	if err = d.Set(DescribeOutputAttributeName, []map[string]any{schemas.NetworkPolicyPropertiesToSchema(policyProperties)}); err != nil {
		return diag.FromErr(err)
	}

	return nil
}

func UpdateContextNetworkPolicy(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*provider.Context).Client
	id := helpers.DecodeSnowflakeID(d.Id()).(sdk.AccountObjectIdentifier)
	set, unset := sdk.NewNetworkPolicySetRequest(), sdk.NewNetworkPolicyUnsetRequest()

	if d.HasChange("name") {
		helpers.EncodeSnowflakeID()
		newId := sdk.NewAccountObjectIdentifier(d.Get("name").(string))

		err := client.NetworkPolicies.Alter(ctx, sdk.NewAlterNetworkPolicyRequest(id).WithRenameTo(newId))
		if err != nil {
			return diag.FromErr(err)
		}

		d.SetId(helpers.EncodeSnowflakeID(newId))
		id = newId
	}

	if d.HasChange("comment") {
		if v, ok := d.GetOk("comment"); ok {
			set.WithComment(v.(string))
		} else {
			unset.WithComment(true)
		}
	}

	if d.HasChange("allowed_network_rule_list") {
		if v, ok := d.GetOk("allowed_network_rule_list"); ok {
			set.WithAllowedNetworkRuleList(*sdk.NewAllowedNetworkRuleListRequest().WithAllowedNetworkRuleList(parseNetworkRulesList(v)))
		} else {
			unset.WithAllowedNetworkRuleList(true)
		}
	}

	if d.HasChange("blocked_network_rule_list") {
		if v, ok := d.GetOk("blocked_network_rule_list"); ok {
			set.WithBlockedNetworkRuleList(*sdk.NewBlockedNetworkRuleListRequest().WithBlockedNetworkRuleList(parseNetworkRulesList(v)))
		} else {
			unset.WithBlockedNetworkRuleList(true)
		}
	}

	if d.HasChange("allowed_ip_list") {
		if v, ok := d.GetOk("allowed_ip_list"); ok {
			set.WithAllowedIpList(*sdk.NewAllowedIPListRequest().WithAllowedIPList(parseIPList(v)))
		} else {
			unset.WithAllowedIpList(true)
		}
	}

	if d.HasChange("blocked_ip_list") {
		if v, ok := d.GetOk("blocked_ip_list"); ok {
			set.WithBlockedIpList(*sdk.NewBlockedIPListRequest().WithBlockedIPList(parseIPList(v)))
		} else {
			unset.WithBlockedIpList(true)
		}
	}

	if !reflect.DeepEqual(*set, *sdk.NewNetworkPolicySetRequest()) {
		if err := client.NetworkPolicies.Alter(ctx, sdk.NewAlterNetworkPolicyRequest(id).WithSet(*set)); err != nil {
			return diag.FromErr(err)
		}
	}

	if !reflect.DeepEqual(*unset, *sdk.NewNetworkPolicyUnsetRequest()) {
		if err := client.NetworkPolicies.Alter(ctx, sdk.NewAlterNetworkPolicyRequest(id).WithUnset(*unset)); err != nil {
			return diag.FromErr(err)
		}
	}

	return ReadContextNetworkPolicy(ctx, d, meta)
}

func DeleteContextNetworkPolicy(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	id := helpers.DecodeSnowflakeID(d.Id()).(sdk.AccountObjectIdentifier)
	client := meta.(*provider.Context).Client

	err := client.NetworkPolicies.Drop(ctx, sdk.NewDropNetworkPolicyRequest(id).WithIfExists(true))
	if err != nil {
		return diag.Diagnostics{
			diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Error deleting network policy",
				Detail:   fmt.Sprintf("Error deleting network policy %v, err = %v", id.Name(), err),
			},
		}
	}

	d.SetId("")
	return nil
}

// parseIPList is a helper function to parse a given ip list from ResourceData.
func parseIPList(v interface{}) []sdk.IPRequest {
	ipList := expandStringList(v.(*schema.Set).List())
	ipRequests := make([]sdk.IPRequest, len(ipList))
	for i, v := range ipList {
		ipRequests[i] = *sdk.NewIPRequest(v)
	}
	return ipRequests
}

// parseNetworkRulesList is a helper function to parse a given network rule list from ResourceData.
func parseNetworkRulesList(v interface{}) []sdk.SchemaObjectIdentifier {
	networkRules := expandStringList(v.(*schema.Set).List())
	networkRuleIdentifiers := make([]sdk.SchemaObjectIdentifier, len(networkRules))
	for i, v := range networkRules {
		networkRuleIdentifiers[i] = sdk.NewSchemaObjectIdentifierFromFullyQualifiedName(v)
	}
	return networkRuleIdentifiers
}
