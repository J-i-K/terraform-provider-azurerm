package applicationinsights

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/appinsights/mgmt/2020-02-02/insights"
	"github.com/hashicorp/terraform-provider-azurerm/helpers/azure"
	"github.com/hashicorp/terraform-provider-azurerm/helpers/tf"
	"github.com/hashicorp/terraform-provider-azurerm/internal/clients"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/applicationinsights/migration"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/applicationinsights/parse"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tags"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/validation"
	"github.com/hashicorp/terraform-provider-azurerm/internal/timeouts"
	"github.com/hashicorp/terraform-provider-azurerm/utils"
)

func resourceApplicationInsights() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Create: resourceApplicationInsightsCreateUpdate,
		Read:   resourceApplicationInsightsRead,
		Update: resourceApplicationInsightsCreateUpdate,
		Delete: resourceApplicationInsightsDelete,

		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := parse.ComponentID(id)
			return err
		}),

		SchemaVersion: 1,
		StateUpgraders: pluginsdk.StateUpgrades(map[int]pluginsdk.StateUpgrade{
			0: migration.ComponentUpgradeV0ToV1{},
		}),

		Timeouts: &pluginsdk.ResourceTimeout{
			Create: pluginsdk.DefaultTimeout(30 * time.Minute),
			Read:   pluginsdk.DefaultTimeout(5 * time.Minute),
			Update: pluginsdk.DefaultTimeout(30 * time.Minute),
			Delete: pluginsdk.DefaultTimeout(30 * time.Minute),
		},

		Schema: map[string]*pluginsdk.Schema{
			"name": {
				Type:     pluginsdk.TypeString,
				Required: true,
				ForceNew: true,
			},

			"resource_group_name": azure.SchemaResourceGroupName(),

			"location": azure.SchemaLocation(),

			"application_type": {
				Type:     pluginsdk.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					"web",
					"other",
					"java",
					"MobileCenter",
					"phone",
					"store",
					"ios",
					"Node.JS",
				}, false),
			},

			"workspace_id": {
				Type:         pluginsdk.TypeString,
				Optional:     true,
				ValidateFunc: azure.ValidateResourceIDOrEmpty,
			},

			"retention_in_days": {
				Type:     pluginsdk.TypeInt,
				Optional: true,
				Default:  90,
				ValidateFunc: validation.IntInSlice([]int{
					30,
					60,
					90,
					120,
					180,
					270,
					365,
					550,
					730,
				}),
			},

			"sampling_percentage": {
				Type:         pluginsdk.TypeFloat,
				Optional:     true,
				Default:      100,
				ValidateFunc: validation.FloatBetween(0, 100),
			},

			"disable_ip_masking": {
				Type:     pluginsdk.TypeBool,
				Optional: true,
				Default:  false,
			},

			"tags": tags.Schema(),

			"daily_data_cap_in_gb": {
				Type:         pluginsdk.TypeFloat,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validation.FloatAtLeast(0),
			},

			"daily_data_cap_notifications_disabled": {
				Type:     pluginsdk.TypeBool,
				Optional: true,
				Computed: true,
			},

			"app_id": {
				Type:     pluginsdk.TypeString,
				Computed: true,
			},

			"instrumentation_key": {
				Type:      pluginsdk.TypeString,
				Computed:  true,
				Sensitive: true,
			},

			"connection_string": {
				Type:      pluginsdk.TypeString,
				Computed:  true,
				Sensitive: true,
			},

			"local_authentication_disabled": {
				Type:     pluginsdk.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func resourceApplicationInsightsCreateUpdate(d *pluginsdk.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).AppInsights.ComponentsClient
	billingClient := meta.(*clients.Client).AppInsights.BillingClient
	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	ctx, cancel := timeouts.ForCreateUpdate(meta.(*clients.Client).StopContext, d)
	defer cancel()

	log.Printf("[INFO] preparing arguments for AzureRM Application Insights creation.")

	name := d.Get("name").(string)
	resGroup := d.Get("resource_group_name").(string)

	resourceId := parse.NewComponentID(subscriptionId, resGroup, name).ID()
	if d.IsNewResource() {
		existing, err := client.Get(ctx, resGroup, name)
		if err != nil {
			if !utils.ResponseWasNotFound(existing.Response) {
				return fmt.Errorf("checking for presence of existing Application Insights %q (Resource Group %q): %s", name, resGroup, err)
			}
		}

		if !utils.ResponseWasNotFound(existing.Response) {
			return tf.ImportAsExistsError("azurerm_application_insights", resourceId)
		}
	}

	applicationType := d.Get("application_type").(string)
	samplingPercentage := utils.Float(d.Get("sampling_percentage").(float64))
	disableIpMasking := d.Get("disable_ip_masking").(bool)
	localAuthenticationDisabled := d.Get("local_authentication_disabled").(bool)
	location := azure.NormalizeLocation(d.Get("location").(string))
	t := d.Get("tags").(map[string]interface{})

	applicationInsightsComponentProperties := insights.ApplicationInsightsComponentProperties{
		ApplicationID:      &name,
		ApplicationType:    insights.ApplicationType(applicationType),
		SamplingPercentage: samplingPercentage,
		DisableIPMasking:   utils.Bool(disableIpMasking),
		DisableLocalAuth:   utils.Bool(localAuthenticationDisabled),
	}

	if workspaceRaw, hasWorkspaceId := d.GetOk("workspace_id"); hasWorkspaceId {
		applicationInsightsComponentProperties.WorkspaceResourceID = utils.String(workspaceRaw.(string))
	}

	if v, ok := d.GetOk("retention_in_days"); ok {
		applicationInsightsComponentProperties.RetentionInDays = utils.Int32(int32(v.(int)))
	}

	insightProperties := insights.ApplicationInsightsComponent{
		Name:                                   &name,
		Location:                               &location,
		Kind:                                   &applicationType,
		ApplicationInsightsComponentProperties: &applicationInsightsComponentProperties,
		Tags:                                   tags.Expand(t),
	}

	_, err := client.CreateOrUpdate(ctx, resGroup, name, insightProperties)
	if err != nil {
		return fmt.Errorf("creating Application Insights %q (Resource Group %q): %+v", name, resGroup, err)
	}

	read, err := client.Get(ctx, resGroup, name)
	if err != nil {
		return fmt.Errorf("retrieving Application Insights %q (Resource Group %q): %+v", name, resGroup, err)
	}
	if read.ID == nil {
		return fmt.Errorf("Cannot read AzureRM Application Insights '%s' (Resource Group %s) ID", name, resGroup)
	}

	billingRead, err := billingClient.Get(ctx, resGroup, name)
	if err != nil {
		return fmt.Errorf("read Application Insights Billing Features %q (Resource Group %q): %+v", name, resGroup, err)
	}

	applicationInsightsComponentBillingFeatures := insights.ApplicationInsightsComponentBillingFeatures{
		CurrentBillingFeatures: billingRead.CurrentBillingFeatures,
		DataVolumeCap:          billingRead.DataVolumeCap,
	}

	if v, ok := d.GetOk("daily_data_cap_in_gb"); ok {
		applicationInsightsComponentBillingFeatures.DataVolumeCap.Cap = utils.Float(v.(float64))
	}

	if v, ok := d.GetOk("daily_data_cap_notifications_disabled"); ok {
		applicationInsightsComponentBillingFeatures.DataVolumeCap.StopSendNotificationWhenHitCap = utils.Bool(v.(bool))
	}

	if _, err = billingClient.Update(ctx, resGroup, name, applicationInsightsComponentBillingFeatures); err != nil {
		return fmt.Errorf("update Application Insights Billing Feature %q (Resource Group %q): %+v", name, resGroup, err)
	}

	d.SetId(resourceId)

	return resourceApplicationInsightsRead(d, meta)
}

func resourceApplicationInsightsRead(d *pluginsdk.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).AppInsights.ComponentsClient
	billingClient := meta.(*clients.Client).AppInsights.BillingClient
	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.ComponentID(d.Id())
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Reading AzureRM Application Insights '%s'", id)

	resp, err := client.Get(ctx, id.ResourceGroup, id.Name)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			d.SetId("")
			return nil
		}
		return fmt.Errorf("making Read request on AzureRM Application Insights '%s': %+v", id.Name, err)
	}

	billingResp, err := billingClient.Get(ctx, id.ResourceGroup, id.Name)
	if err != nil {
		return fmt.Errorf("making Read request on AzureRM Application Insights Billing Feature '%s': %+v", id.Name, err)
	}

	d.Set("name", id.Name)
	d.Set("resource_group_name", id.ResourceGroup)
	if location := resp.Location; location != nil {
		d.Set("location", azure.NormalizeLocation(*location))
	}

	if props := resp.ApplicationInsightsComponentProperties; props != nil {
		// Accommodate application_type that only differs by case and so shouldn't cause a recreation
		vals := map[string]string{
			"web":   "web",
			"other": "other",
		}
		if v, ok := vals[strings.ToLower(string(props.ApplicationType))]; ok {
			d.Set("application_type", v)
		} else {
			d.Set("application_type", string(props.ApplicationType))
		}
		d.Set("app_id", props.AppID)
		d.Set("instrumentation_key", props.InstrumentationKey)
		d.Set("sampling_percentage", props.SamplingPercentage)
		d.Set("disable_ip_masking", props.DisableIPMasking)
		d.Set("connection_string", props.ConnectionString)
		d.Set("local_authentication_disabled", props.DisableLocalAuth)

		if v := props.WorkspaceResourceID; v != nil {
			d.Set("workspace_id", v)
		}

		if v := props.RetentionInDays; v != nil {
			d.Set("retention_in_days", v)
		}
	}

	if billingProps := billingResp.DataVolumeCap; billingProps != nil {
		d.Set("daily_data_cap_in_gb", billingProps.Cap)
		d.Set("daily_data_cap_notifications_disabled", billingProps.StopSendNotificationWhenHitCap)
	}

	return tags.FlattenAndSet(d, resp.Tags)
}

func resourceApplicationInsightsDelete(d *pluginsdk.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).AppInsights.ComponentsClient
	ctx, cancel := timeouts.ForDelete(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.ComponentID(d.Id())
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Deleting AzureRM Application Insights %q (resource group %q)", id.Name, id.ResourceGroup)

	resp, err := client.Delete(ctx, id.ResourceGroup, id.Name)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("issuing AzureRM delete request for Application Insights %q: %+v", id.Name, err)
	}

	return err
}
