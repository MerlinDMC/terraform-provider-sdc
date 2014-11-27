package provider

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/joyent/gocommon/errors"
	"github.com/joyent/gocommon/http"
	"github.com/joyent/gosdc/cloudapi"
)

func resourceComputeInstance() *schema.Resource {
	return &schema.Resource{
		Create: resourceComputeInstanceCreate,
		Read:   resourceComputeInstanceRead,
		// Update: resourceComputeInstanceUpdate,
		Update: nil,
		Delete: resourceComputeInstanceDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},

			"image": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},

			"package": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},

			"network": &schema.Schema{
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"source": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"name": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
						},

						"public": &schema.Schema{
							Type:     schema.TypeBool,
							Computed: true,
						},

						"address": &schema.Schema{
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},

			"metadata": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Schema{
					Type: schema.TypeMap,
				},
			},

			"tags": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Schema{
					Type: schema.TypeMap,
				},
			},

			"primary_ip": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"state": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"type": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"memory": &schema.Schema{
				Type:     schema.TypeInt,
				Computed: true,
			},

			"disk": &schema.Schema{
				Type:     schema.TypeInt,
				Computed: true,
			},

			"created": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},

			"updated": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceComputeInstanceCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	opts := cloudapi.CreateMachineOpts{
		Name:     d.Get("name").(string),
		Image:    d.Get("image").(string),
		Package:  d.Get("package").(string),
		Metadata: computeInstanceMetadata(d),
		Tags:     computeInstanceTags(d),
	}

	// Add configured networks
	networksCount := d.Get("network.#").(int)
	for i := 0; i < networksCount; i++ {
		prefix := fmt.Sprintf("network.%d", i)

		// Load up the uuid of this network out of the source setting
		networkUuid := d.Get(prefix + ".source").(string)

		// Test if the given uuid is valid
		if network, err := config.sdc_client.GetNetwork(networkUuid); err != nil {
			return fmt.Errorf(
				"Error adding network '%s': %s",
				networkUuid, err)
		} else {
			d.Set(prefix+".public", network.Public)

			opts.Networks = append(opts.Networks, network.Id)
		}
	}

	machine, err := config.sdc_client.CreateMachine(opts)
	if err != nil {
		return fmt.Errorf("Error creating instance: %s", err)
	}

	d.SetId(machine.Id)

	stateConf := &resource.StateChangeConf{
		Pending:    []string{"provisioning"},
		Target:     "running",
		Refresh:    computeInstanceStateRefreshFunc(config.sdc_client, machine.Id),
		Timeout:    10 * time.Minute,
		Delay:      10 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	machineRaw, err := stateConf.WaitForState()
	if err != nil {
		return fmt.Errorf(
			"Error waiting for instance (%s) to become ready: %s",
			machine.Id, err)
	}

	computeInstanceUpdateMeta(d, machineRaw.(*cloudapi.Machine))

	return nil
}

func resourceComputeInstanceRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	machine, err := config.sdc_client.GetMachine(d.Id())
	if err != nil {
		if errorIsMachineGoneError(err) {
			// Machine is gone already
			d.SetId("")

			return nil
		}

		return fmt.Errorf("Error reading instance: %s", err)
	}

	computeInstanceUpdateMeta(d, machine)

	return nil
}

func resourceComputeInstanceUpdate(d *schema.ResourceData, meta interface{}) error {
	return resourceComputeInstanceRead(d, meta)
}

func resourceComputeInstanceDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)

	err := config.sdc_client.DeleteMachine(d.Id())
	if err != nil {
		return fmt.Errorf("Error deleting instance: %s", err)
	}

	stateConf := &resource.StateChangeConf{
		Pending:    []string{"provisioning", "running", "stopping"},
		Target:     "stopped",
		Refresh:    computeInstanceStateRefreshFunc(config.sdc_client, d.Id()),
		Timeout:    10 * time.Minute,
		Delay:      10 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	_, err = stateConf.WaitForState()
	if err != nil && !errorIsMachineGoneError(err) {
		return fmt.Errorf(
			"Error waiting for instance (%s) to be deleted: %s",
			d.Id(), err)
	}

	d.SetId("")

	return nil
}

func computeInstanceTags(d *schema.ResourceData) map[string]string {
	var tags map[string]string

	if v := d.Get("tags").([]interface{}); len(v) > 0 {
		tags = make(map[string]string)

		for _, v := range v {
			for k, v := range v.(map[string]interface{}) {
				if strings.HasPrefix(k, "tag.") {
					tags[k] = v.(string)
				} else {
					tags["tag."+k] = v.(string)
				}
			}
		}
	}

	return tags
}

func computeInstanceMetadata(d *schema.ResourceData) map[string]string {
	var metadata map[string]string

	if v := d.Get("metadata").([]interface{}); len(v) > 0 {
		metadata = make(map[string]string)

		for _, v := range v {
			for k, v := range v.(map[string]interface{}) {
				if strings.HasPrefix(k, "metadata.") {
					metadata[k] = v.(string)
				} else {
					metadata["metadata."+k] = v.(string)
				}
			}
		}
	}

	return metadata
}

func computeInstanceUpdateMeta(d *schema.ResourceData, machine *cloudapi.Machine) {
	d.Set("primary_ip", machine.PrimaryIP)
	d.Set("state", machine.State)
	d.Set("type", machine.Type)
	d.Set("memory", machine.Memory)
	d.Set("disk", machine.Disk)
	d.Set("created", machine.Created)
	d.Set("updated", machine.Updated)

	// match the list of networks + IPs
	networksCount := d.Get("network.#").(int)
	for i := 0; i < networksCount; i++ {
		prefix := fmt.Sprintf("network.%d", i)

		d.Set(prefix+".address", machine.IPs[i])
	}

	if machine.PrimaryIP != "" {
		d.SetConnInfo(map[string]string{
			"type": "ssh",
			"host": machine.PrimaryIP,
		})
	}
}

func computeInstanceStateRefreshFunc(client *cloudapi.Client, machineId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		machine, err := client.GetMachine(machineId)
		if err != nil {
			return nil, "", err
		}

		return machine, machine.State, nil
	}
}

func errorIsMachineGoneError(err error) bool {
	if errors.IsUnknownError(err) {
		if err, ok := getEncapsulatedHttpError(err); ok {
			if err.StatusCode == 410 {
				return true
			}
		}
	}

	return false
}

func getEncapsulatedHttpError(err error) (*http.HttpError, bool) {
	var ok bool
	var e errors.Error

	for {
		if e, ok = err.(errors.Error); ok {
			if e.Cause() != nil {
				err = e.Cause()
			} else {
				break
			}
		} else if e, ok := err.(*http.HttpError); ok {
			return e, true
		}
	}

	return nil, false
}
