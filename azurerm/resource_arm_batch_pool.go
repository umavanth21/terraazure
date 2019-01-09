package azurerm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/suppress"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/validate"

	"github.com/Azure/azure-sdk-for-go/services/batch/mgmt/2017-09-01/batch"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/response"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmBatchPool() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmBatchPoolCreate,
		Read:   resourceArmBatchPoolRead,
		Update: resourceArmBatchPoolUpdate,
		Delete: resourceArmBatchPoolDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: azure.ValidateAzureRMBatchPoolName,
			},
			"resource_group_name": resourceGroupNameSchema(),
			"account_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validateAzureRMBatchAccountName,
			},
			"display_name": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"vm_size": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				DiffSuppressFunc: suppress.CaseDifference,
			},
			"fixed_scale": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"target_dedicated_nodes": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      1,
							ValidateFunc: validation.IntBetween(0, 2000),
						},
						"target_low_priority_nodes": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      0,
							ValidateFunc: validation.IntBetween(0, 1000),
						},
						"resize_timeout": {
							Type:     schema.TypeString,
							Optional: true,
							Default:  "PT15M",
						},
					},
				},
			},
			"auto_scale": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"evaluation_interval": {
							Type:     schema.TypeString,
							Optional: true,
							Default:  "PT15M",
						},
						"formula": {
							Type:     schema.TypeString,
							Required: true,
							DiffSuppressFunc: func(_, old, new string, d *schema.ResourceData) bool {
								return strings.TrimSpace(old) == strings.TrimSpace(new)
							},
						},
					},
				},
			},
			"storage_image_reference": {
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: azure.ValidateResourceID,
						},

						"publisher": {
							Type:         schema.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},

						"offer": {
							Type:         schema.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},

						"sku": {
							Type:             schema.TypeString,
							Required:         true,
							ForceNew:         true,
							DiffSuppressFunc: suppress.CaseDifference,
							ValidateFunc:     validate.NoEmptyStrings,
						},

						"version": {
							Type:         schema.TypeString,
							Required:     true,
							ForceNew:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},
					},
				},
			},
			"node_agent_sku_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"stop_pending_resize_operation": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"start_task": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"command_line": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validate.NoEmptyStrings,
						},

						"max_task_retry_count": {
							Type:     schema.TypeInt,
							Optional: true,
							Default:  1,
						},

						"wait_for_success": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},

						"environment": {
							Type:     schema.TypeMap,
							Optional: true,
						},

						"user_identity": {
							Type:     schema.TypeList,
							Required: true,
							MaxItems: 1,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"user_name": {
										Type:     schema.TypeString,
										Optional: true,
									},
									"auto_user": {
										Type:     schema.TypeList,
										Optional: true,
										MaxItems: 1,
										Elem: &schema.Resource{
											Schema: map[string]*schema.Schema{
												"elevation_level": {
													Type:     schema.TypeString,
													Optional: true,
													Default:  string(batch.NonAdmin),
													ValidateFunc: validation.StringInSlice([]string{
														string(batch.NonAdmin),
														string(batch.Admin),
													}, false),
												},
												"scope": {
													Type:     schema.TypeString,
													Optional: true,
													Default:  string(batch.AutoUserScopeTask),
													ValidateFunc: validation.StringInSlice([]string{
														string(batch.AutoUserScopeTask),
														string(batch.AutoUserScopePool),
													}, false),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func resourceArmBatchPoolCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient).batchPoolClient
	ctx := meta.(*ArmClient).StopContext

	log.Printf("[INFO] preparing arguments for Azure Batch pool creation.")

	resourceGroup := d.Get("resource_group_name").(string)
	accountName := d.Get("account_name").(string)
	poolName := d.Get("name").(string)
	displayName := d.Get("display_name").(string)
	vmSize := d.Get("vm_size").(string)

	parameters := batch.Pool{
		PoolProperties: &batch.PoolProperties{
			VMSize:      &vmSize,
			DisplayName: &displayName,
		},
	}

	scaleSettings, err := expandBatchPoolScaleSettings(d)
	if err != nil {
		return fmt.Errorf("Error expanding scale settings: %+v", err)
	}

	parameters.PoolProperties.ScaleSettings = scaleSettings

	nodeAgentSkuID := d.Get("node_agent_sku_id").(string)

	storageImageReferenceSet := d.Get("storage_image_reference").([]interface{})
	imageReference, err := azure.ExpandBatchPoolImageReference(storageImageReferenceSet)
	if err != nil {
		return fmt.Errorf("Error creating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	if startTaskValue, startTaskOk := d.GetOk("start_task"); startTaskOk {
		startTaskList := startTaskValue.([]interface{})
		startTask, startTaskErr := azure.ExpandBatchPoolStartTask(startTaskList)

		if startTaskErr != nil {
			return fmt.Errorf("Error creating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, startTaskErr)
		}

		// start task should have a user identity defined
		userIdentity := startTask.UserIdentity
		if userIdentityError := validateUserIdentity(userIdentity); userIdentityError != nil {
			return fmt.Errorf("Error creating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, userIdentityError)
		}

		parameters.PoolProperties.StartTask = startTask
	}

	parameters.PoolProperties.DeploymentConfiguration = &batch.DeploymentConfiguration{
		VirtualMachineConfiguration: &batch.VirtualMachineConfiguration{
			NodeAgentSkuID: &nodeAgentSkuID,
			ImageReference: imageReference,
		},
	}

	future, err := client.Create(ctx, resourceGroup, accountName, poolName, parameters, "", "")
	if err != nil {
		return fmt.Errorf("Error creating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("Error waiting for creation of Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	read, err := client.Get(ctx, resourceGroup, accountName, poolName)
	if err != nil {
		return fmt.Errorf("Error retrieving Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	if read.ID == nil {
		return fmt.Errorf("Cannot read Batch pool %q (resource group %q) ID", poolName, resourceGroup)
	}

	d.SetId(*read.ID)

	// if the pool is not Steady after the create operation, wait for it to be Steady
	if props := read.PoolProperties; props != nil && props.AllocationState != batch.Steady {
		if err = waitForBatchPoolPendingResizeOperation(ctx, client, resourceGroup, accountName, poolName); err != nil {
			return fmt.Errorf("Error waiting for Batch pool %q (resource group %q) being ready", poolName, resourceGroup)
		}
	}

	return resourceArmBatchPoolRead(d, meta)
}

func resourceArmBatchPoolUpdate(d *schema.ResourceData, meta interface{}) error {
	ctx := meta.(*ArmClient).StopContext
	client := meta.(*ArmClient).batchPoolClient

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	poolName := id.Path["pools"]
	accountName := id.Path["batchAccounts"]

	resp, err := client.Get(ctx, resourceGroup, accountName, poolName)
	if err != nil {
		return fmt.Errorf("Error retrieving the Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	if resp.PoolProperties.AllocationState != batch.Steady {
		log.Printf("[INFO] there is a pending resize operation on this pool...")
		stopPendingResizeOperation := d.Get("stop_pending_resize_operation").(bool)
		if !stopPendingResizeOperation {
			return fmt.Errorf("Error updating the Batch pool %q (Resource Group %q) because of pending resize operation. Set flag `stop_pending_resize_operation` to true to force update", poolName, resourceGroup)
		}

		log.Printf("[INFO] stopping the pending resize operation on this pool...")
		if _, err = client.StopResize(ctx, resourceGroup, accountName, poolName); err != nil {
			return fmt.Errorf("Error stopping resize operation for Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
		}

		// waiting for the pool to be in steady state
		if err = waitForBatchPoolPendingResizeOperation(ctx, client, resourceGroup, accountName, poolName); err != nil {
			return fmt.Errorf("Error waiting for Batch pool %q (resource group %q) being ready", poolName, resourceGroup)
		}
	}

	parameters := batch.Pool{
		PoolProperties: &batch.PoolProperties{},
	}

	scaleSettings, err := expandBatchPoolScaleSettings(d)
	if err != nil {
		return fmt.Errorf("Error expanding scale settings: %+v", err)
	}

	parameters.PoolProperties.ScaleSettings = scaleSettings

	if startTaskValue, startTaskOk := d.GetOk("start_task"); startTaskOk {
		startTaskList := startTaskValue.([]interface{})
		startTask, startTaskErr := azure.ExpandBatchPoolStartTask(startTaskList)

		if startTaskErr != nil {
			return fmt.Errorf("Error updating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, startTaskErr)
		}

		// start task should have a user identity defined
		userIdentity := startTask.UserIdentity
		if userIdentityError := validateUserIdentity(userIdentity); userIdentityError != nil {
			return fmt.Errorf("Error creating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, userIdentityError)
		}

		parameters.PoolProperties.StartTask = startTask
	}

	result, err := client.Update(ctx, resourceGroup, accountName, poolName, parameters, "")
	if err != nil {
		return fmt.Errorf("Error updating Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	// if the pool is not Steady after the update, wait for it to be Steady
	if props := result.PoolProperties; props != nil && props.AllocationState != batch.Steady {
		if err := waitForBatchPoolPendingResizeOperation(ctx, client, resourceGroup, accountName, poolName); err != nil {
			return fmt.Errorf("Error waiting for Batch pool %q (resource group %q) being ready", poolName, resourceGroup)
		}
	}

	return resourceArmBatchPoolRead(d, meta)
}

func resourceArmBatchPoolRead(d *schema.ResourceData, meta interface{}) error {
	ctx := meta.(*ArmClient).StopContext
	client := meta.(*ArmClient).batchPoolClient

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	poolName := id.Path["pools"]
	accountName := id.Path["batchAccounts"]

	resp, err := client.Get(ctx, resourceGroup, accountName, poolName)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			return fmt.Errorf("Error: Batch pool %q in account %q (Resource Group %q) was not found", poolName, accountName, resourceGroup)
		}
		return fmt.Errorf("Error making Read request on AzureRM Batch pool %q: %+v", poolName, err)
	}

	d.Set("name", poolName)
	d.Set("account_name", accountName)
	d.Set("resource_group_name", resourceGroup)

	if props := resp.PoolProperties; props != nil {
		d.Set("vm_size", props.VMSize)

		if scaleSettings := props.ScaleSettings; scaleSettings != nil {
			if err := d.Set("auto_scale", azure.FlattenBatchPoolAutoScaleSettings(scaleSettings.AutoScale)); err != nil {
				return fmt.Errorf("Error flattening `auto_scale`: %+v", err)
			}
			if err := d.Set("fixed_scale", azure.FlattenBatchPoolFixedScaleSettings(scaleSettings.FixedScale)); err != nil {
				return fmt.Errorf("Error flattening `fixed_scale `: %+v", err)
			}
		}

		if props.DeploymentConfiguration != nil &&
			props.DeploymentConfiguration.VirtualMachineConfiguration != nil &&
			props.DeploymentConfiguration.VirtualMachineConfiguration.ImageReference != nil {

			imageReference := props.DeploymentConfiguration.VirtualMachineConfiguration.ImageReference

			d.Set("storage_image_reference", azure.FlattenBatchPoolImageReference(imageReference))
			d.Set("node_agent_sku_id", props.DeploymentConfiguration.VirtualMachineConfiguration.NodeAgentSkuID)
		}

		d.Set("start_task", azure.FlattenBatchPoolStartTask(props.StartTask))
	}

	return nil
}

func resourceArmBatchPoolDelete(d *schema.ResourceData, meta interface{}) error {
	ctx := meta.(*ArmClient).StopContext
	client := meta.(*ArmClient).batchPoolClient

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resourceGroup := id.ResourceGroup
	poolName := id.Path["pools"]
	accountName := id.Path["batchAccounts"]

	future, err := client.Delete(ctx, resourceGroup, accountName, poolName)
	if err != nil {
		return fmt.Errorf("Error deleting Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		if !response.WasNotFound(future.Response()) {
			return fmt.Errorf("Error waiting for deletion of Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
		}
	}
	return nil
}

func expandBatchPoolScaleSettings(d *schema.ResourceData) (*batch.ScaleSettings, error) {
	scaleSettings := &batch.ScaleSettings{}

	autoScaleValue, autoScaleOk := d.GetOk("auto_scale")
	fixedScaleValue, fixedScaleOk := d.GetOk("fixed_scale")

	if !autoScaleOk && !fixedScaleOk {
		return nil, fmt.Errorf("Error: auto_scale block or fixed_scale block need to be specified")
	}

	if autoScaleOk && fixedScaleOk {
		return nil, fmt.Errorf("Error: auto_scale and fixed_scale blocks cannot be specified at the same time")
	}

	if autoScaleOk {
		autoScale := autoScaleValue.([]interface{})
		if len(autoScale) == 0 {
			return nil, fmt.Errorf("Error: when scale mode is Auto, auto_scale block is required")
		}

		autoScaleSettings := autoScale[0].(map[string]interface{})

		autoScaleEvaluationInterval := autoScaleSettings["evaluation_interval"].(string)
		autoScaleFormula := autoScaleSettings["formula"].(string)

		scaleSettings.AutoScale = &batch.AutoScaleSettings{
			EvaluationInterval: &autoScaleEvaluationInterval,
			Formula:            &autoScaleFormula,
		}
	} else if fixedScaleOk {
		fixedScale := fixedScaleValue.([]interface{})
		if len(fixedScale) == 0 {
			return nil, fmt.Errorf("Error: when scale mode is Fixed, fixed_scale block is required")
		}

		fixedScaleSettings := fixedScale[0].(map[string]interface{})

		targetDedicatedNodes := int32(fixedScaleSettings["target_dedicated_nodes"].(int))
		targetLowPriorityNodes := int32(fixedScaleSettings["target_low_priority_nodes"].(int))
		resizeTimeout := fixedScaleSettings["resize_timeout"].(string)

		scaleSettings.FixedScale = &batch.FixedScaleSettings{
			ResizeTimeout:          &resizeTimeout,
			TargetDedicatedNodes:   &targetDedicatedNodes,
			TargetLowPriorityNodes: &targetLowPriorityNodes,
		}
	}

	return scaleSettings, nil
}

func waitForBatchPoolPendingResizeOperation(ctx context.Context, client batch.PoolClient, resourceGroup string, accountName string, poolName string) error {
	// waiting for the pool to be in steady state
	log.Printf("[INFO] waiting for the pending resize operation on this pool to be stopped...")
	isSteady := false
	for !isSteady {
		resp, err := client.Get(ctx, resourceGroup, accountName, poolName)
		if err != nil {
			return fmt.Errorf("Error retrieving the Batch pool %q (Resource Group %q): %+v", poolName, resourceGroup, err)
		}

		isSteady = resp.PoolProperties.AllocationState == batch.Steady
		time.Sleep(time.Second * 30)
		log.Printf("[INFO] waiting for the pending resize operation on this pool to be stopped... New try in 30 seconds...")
	}

	return nil
}

// validateUserIdentity validates that the user identity for a start task has been well specified
// it should have a auto_user block or a user_name defined, but not both at the same time.
func validateUserIdentity(userIdentity *batch.UserIdentity) error {
	if userIdentity == nil {
		return errors.New("user_identity block needs to be specified")
	}

	if userIdentity.AutoUser == nil && userIdentity.UserName == nil {
		return errors.New("auto_user or user_name needs to be specified in the user_identity block")
	}

	if userIdentity.AutoUser != nil && userIdentity.UserName != nil {
		return errors.New("auto_user and user_name cannot be specified in the user_identity at the same time")
	}

	return nil
}