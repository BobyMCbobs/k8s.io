package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssdkconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// consts
const (
	// formats
	usageDateFormat  = "2006-01-02"
	resultDateFormat = "200601021504"
)

var (
	now           = time.Now()
	defaultConfig = awsCostExplorerExportConfig{
		AWSRegion: "us-east-1",
		DateInput: time.Now().Format(usageDateFormat),
	}
)

type costReport struct {
	awsCostExplorerExportConfig awsCostExplorerExportConfig
}

// AWSCostExplorerExportConfig stores configuration for the runtime
type awsCostExplorerExportConfig struct {
	AWSRegion string
	DateInput string

	// clients
	ceclient *costexplorer.Client
}

// usageClient stores the client for costexplorer
type usageClient struct {
	client *costexplorer.Client
	config costReport
}

// ResultByTime is a recreation of the CostExplorer types is required
// it stores the amount for each day a bill was made
type resultByTime struct {
	ID              string
	Estimated       bool
	Quantity        string
	TimePeriodStart string
	TimePeriodEnd   string
	Unit            string
	Amount          string
	ProjectID       string
	ProjectName     string
}

func Pointer[V any](input V) *V {
	return &input
}

// GetInputForUsage returns an input for making the cost and usage data request
func (c usageClient) GetAWSCostExplorerInputForUsage(nextPageToken *string, dateInput string) *costexplorer.GetCostAndUsageInput {
	input, err := time.Parse(usageDateFormat, dateInput)
	if err != nil {
		panic(err)
	}
	start := input.Add(-(time.Hour * 24 * 8)).Format(usageDateFormat)
	end := input.Add(-(time.Hour * 24)).Format(usageDateFormat)
	return &costexplorer.GetCostAndUsageInput{
		Filter: &cetypes.Expression{
			Not: &cetypes.Expression{
				Dimensions: &cetypes.DimensionValues{
					Key:          cetypes.DimensionPurchaseType,
					MatchOptions: []cetypes.MatchOption{cetypes.MatchOptionEquals},
					Values:       []string{"Refund", "Credit"},
				},
			},
		},
		Metrics:     []string{string(cetypes.MetricUnblendedCost), string(cetypes.MetricUsageQuantity)},
		Granularity: cetypes.GranularityMonthly,
		GroupBy: []cetypes.GroupDefinition{{
			Type: cetypes.GroupDefinitionTypeDimension,
			Key:  aws.String(string(cetypes.DimensionLinkedAccount)),
		}},
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(start),
			End:   aws.String(end),
		},
		NextPageToken: nextPageToken,
	}
}

func (c usageClient) GetAWSWeeklyCost() (total float64, err error) {
	var nextPageToken *string
	costAndUsageOutput := &costexplorer.GetCostAndUsageOutput{}

	log.Println("fetching cost usage data from AWS")
	for page := 0; true; page++ {
		input := c.GetAWSCostExplorerInputForUsage(nextPageToken, c.config.awsCostExplorerExportConfig.DateInput)
		usage, err := c.client.GetCostAndUsage(context.TODO(), input)
		if err != nil {
			panic(err)
		}
		if usage == nil {
			break
		}
		costAndUsageOutput.DimensionValueAttributes = append(costAndUsageOutput.DimensionValueAttributes, usage.DimensionValueAttributes...)
		costAndUsageOutput.ResultsByTime = append(costAndUsageOutput.ResultsByTime, usage.ResultsByTime...)
		if usage.NextPageToken == nil {
			break
		}
		nextPageToken = usage.NextPageToken
	}

	dimensionValueAttributes := map[string]string{}
outerLoop:
	for _, v := range costAndUsageOutput.DimensionValueAttributes {
		for id, description := range dimensionValueAttributes {
			if *v.Value == id && v.Attributes["description"] == description {
				log.Printf("Found duplicate of '%v = %v', skipping...\n", id, description)
				continue outerLoop
			}
		}
		if v.Value == nil || *v.Value == "" {
			log.Println("Found empty value field, skipping...")
			continue outerLoop
		} else if v.Attributes["description"] == "" {
			log.Println("Found empty description field, setting as 'UNKNOWN'")
			v.Attributes["description"] = "UNKNOWN"
		}
		dimensionValueAttributes[*v.Value] = v.Attributes["description"]
	}

	resultsByTime := []resultByTime{}
	for _, value := range costAndUsageOutput.ResultsByTime {
		for k, v := range value.Total {
			fmt.Println("TOTALS: ", k, v)
		}
		for _, g := range value.Groups {
			resultByTime := resultByTime{
				Estimated:       value.Estimated,
				Amount:          *g.Metrics["UnblendedCost"].Amount,
				Unit:            *g.Metrics["UnblendedCost"].Unit,
				Quantity:        *g.Metrics["UsageQuantity"].Amount,
				TimePeriodStart: *value.TimePeriod.Start,
				TimePeriodEnd:   *value.TimePeriod.End,
				ProjectID:       g.Keys[0],
				ProjectName:     dimensionValueAttributes[g.Keys[0]],
			}
			fmt.Println(resultByTime.Amount, resultByTime.Unit, resultByTime.Quantity, resultByTime.ProjectName)
			resultsByTime = append(resultsByTime, resultByTime)
			amount, err := strconv.ParseFloat(resultByTime.Amount, 64)
			if err != nil {
				panic(err)
			}
			quantity, err := strconv.ParseFloat(resultByTime.Quantity, 64)
			if err != nil {
				panic(err)
			}
			total += amount * quantity
		}
	}
	return total, nil
}

func (c usageClient) GetGCPWeeklyCost() (total float64, err error) {
	return total, nil
}

func main() {
	var config costReport
	flag.StringVar(&config.awsCostExplorerExportConfig.AWSRegion, "aws-region", defaultConfig.AWSRegion, "specify an AWS region")
	flag.StringVar(&config.awsCostExplorerExportConfig.DateInput, "input-date", defaultConfig.DateInput, "specify the amount of days back to report from today")
	flag.Parse()

	cfg, err := awssdkconfig.LoadDefaultConfig(context.TODO(),
		awssdkconfig.WithRegion(config.awsCostExplorerExportConfig.AWSRegion),
	)
	if err != nil {
		log.Printf("unable to load SDK config, %v", err)
		return
	}

	ceclient := costexplorer.NewFromConfig(cfg)
	uc := usageClient{client: ceclient, config: config}

	gcpTotal, err := uc.GetGCPWeeklyCost()
	if err != nil {
		panic(err)
	}
	fmt.Println("GCP Total   : ", gcpTotal)

	awsTotal, err := uc.GetAWSWeeklyCost()
	if err != nil {
		panic(err)
	}
	fmt.Println("AWS Total   : ", awsTotal)
}
