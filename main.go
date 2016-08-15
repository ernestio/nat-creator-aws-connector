/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	ecc "github.com/ernestio/ernest-config-client"
	"github.com/nats-io/nats"
)

var nc *nats.Conn
var natsErr error

func eventHandler(m *nats.Msg) {
	var n Event

	err := n.Process(m.Data)
	if err != nil {
		return
	}

	if err = n.Validate(); err != nil {
		n.Error(err)
		return
	}

	err = createNat(&n)
	if err != nil {
		n.Error(err)
		return
	}

	n.Complete()
}

func internetGatewayByVPCID(svc *ec2.EC2, id string) (*ec2.InternetGateway, error) {
	f := []*ec2.Filter{
		&ec2.Filter{
			Name:   aws.String("attachment.vpc-id"),
			Values: []*string{aws.String(id)},
		},
	}

	req := ec2.DescribeInternetGatewaysInput{
		Filters: f,
	}

	resp, err := svc.DescribeInternetGateways(&req)
	if err != nil {
		return nil, err
	}

	if len(resp.InternetGateways) == 0 {
		return nil, nil
	}

	return resp.InternetGateways[0], nil
}

func createInternetGateway(svc *ec2.EC2, id string) (string, error) {
	ig, err := internetGatewayByVPCID(svc, id)
	if err != nil {
		return "", err
	}

	if ig != nil {
		return *ig.InternetGatewayId, nil
	}

	resp, err := svc.CreateInternetGateway(nil)
	if err != nil {
		return "", err
	}

	req := ec2.AttachInternetGatewayInput{
		InternetGatewayId: resp.InternetGateway.InternetGatewayId,
		VpcId:             aws.String(id),
	}

	_, err = svc.AttachInternetGateway(&req)
	if err != nil {
		return "", err
	}

	return *resp.InternetGateway.InternetGatewayId, nil
}

func createNat(ev *Event) error {
	creds := credentials.NewStaticCredentials(ev.DatacenterAccessKey, ev.DatacenterAccessToken, "")
	svc := ec2.New(session.New(), &aws.Config{
		Region:      aws.String(ev.DatacenterRegion),
		Credentials: creds,
	})

	// Create Elastic IP
	resp, err := svc.AllocateAddress(nil)
	if err != nil {
		return err
	}

	ev.NatGatewayAllocationID = *resp.AllocationId
	ev.NatGatewayAllocationIP = *resp.PublicIp

	// Create Internet Gateway
	ev.InternetGatewayID, err = createInternetGateway(svc, ev.DatacenterVPCID)
	if err != nil {
		return err
	}

	// Create Nat Gateway
	req := ec2.CreateNatGatewayInput{
		AllocationId: aws.String(ev.NatGatewayAllocationID),
		SubnetId:     aws.String(ev.NetworkAWSID),
	}

	gwresp, err := svc.CreateNatGateway(&req)
	if err != nil {
		return err
	}

	ev.NatGatewayAWSID = *gwresp.NatGateway.NatGatewayId

	return nil
}

func main() {
	nc = ecc.NewConfig(os.Getenv("NATS_URI")).Nats()

	fmt.Println("listening for nat.create.aws")
	nc.Subscribe("nat.create.aws", eventHandler)

	runtime.Goexit()
}
