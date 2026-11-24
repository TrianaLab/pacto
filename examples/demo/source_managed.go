package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// This file holds the managed cloud resources the demo cluster's operator
// read: two Crossplane RDS instances and one queue from a listed Terraform
// state, the Secret names the workloads that use them reference, and the
// three Data sources rows that say how they were read. It has no build tag,
// so the TestDemoManaged tests build a snapshot from it on the host;
// buildDemoFleet feeds it to the browser demo. Nothing here is a published
// bundle.

// demoAWSAccount and demoAWSRegion are the account and region the demo's AWS
// resources live in.
const (
	demoAWSAccount = "123456789012"
	demoAWSRegion  = "eu-west-1"
)

// The demo's managed external systems.
const (
	// demoOrdersDB is the RDS instance nobody provisioned through an
	// offering: two shop workloads read its connection Secret.
	demoOrdersDB = fleet.ExternalKey("aws/rds/" + demoAWSAccount + "/" + demoAWSRegion + "/orders-db")
	// demoTicketingDB is the RDS instance the platform's PostgreSQLInstance
	// claim produced for ticketing-service in staging.
	demoTicketingDB = fleet.ExternalKey("aws/rds/" + demoAWSAccount + "/" + demoAWSRegion + "/ticketing-db")
	// demoOrderEvents is the SQS queue a listed Terraform state holds.
	demoOrderEvents = fleet.ExternalKey("aws/sqs/" + demoAWSAccount + "/" + demoAWSRegion + "/order-events")
)

// demoTicketingClaim is the claim behind ticketing-db, as the operator
// verified it and as the platform adapter names it in its report.
var demoTicketingClaim = fleet.ManagedObjectRef("PostgreSQLInstance", "staging", "ticketing-db")

// demoTerraformState is the state location a person listed in
// controller.terraformStates.
const demoTerraformState = "s3://acme-tf/prod.tfstate?region=" + demoAWSRegion

// demoOperatorIdentity is the read-only workload identity bound to the demo
// operator's ServiceAccount for registry pushes. Cloud inventory stays off:
// nobody named the cloud in controller.cloudInventory.
const demoOperatorIdentity = "aws identity arn:aws:iam::" + demoAWSAccount + ":role/pacto-operator"

// withDemoManaged is res with what the operator read of managed resources on
// cluster: the resources and the Secret refs, each stamped with the cluster
// as the detection source stamps them, and the managed-resources, terraform
// and cloud-inventory rows.
func withDemoManaged(res detect.Result, cluster *fleet.Cluster) detect.Result {
	read := demoNow().Add(-20 * time.Minute)
	cat := externalcatalog.DefaultDigest()
	rds := func(name, engine string, secrets []string, claim string) fleet.RawManagedResource {
		return fleet.RawManagedResource{Source: "crossplane", Identity: "aws/rds/" + demoAWSAccount + "/" + demoAWSRegion + "/" + name,
			Entry: "aws/rds", Catalog: cat, Type: "rds.aws.upbound.io/Instance", Name: name,
			ConnectionSecrets: secrets, Claim: claim, Engine: engine, ObservedAt: &read}
	}
	res.ManagedResources = []fleet.RawManagedResource{
		rds("orders-db", "postgres 16", []string{"shop/orders-db-conn"}, ""),
		rds("ticketing-db", "postgres 16", []string{"staging/ticketing-db-conn"}, demoTicketingClaim),
		{Source: "terraform", Identity: string(demoOrderEvents), Entry: "aws/sqs", Catalog: cat, Type: "aws_sqs_queue",
			Name: "aws_sqs_queue.order_events", Location: demoTerraformState, ObservedAt: &read},
	}
	res.ManagedResourcesTotal = len(res.ManagedResources)
	// The two shop workloads with no contract read orders-db's connection Secret.
	res.SecretRefs = []fleet.RawSecretRef{
		{Namespace: "shop", Kind: "Deployment", Name: "legacy-billing", Secret: "orders-db-conn"},
		{Namespace: "shop", Kind: "Deployment", Name: "billing-worker", Secret: "orders-db-conn"},
	}
	for i := range res.ManagedResources {
		res.ManagedResources[i].Cluster, res.ManagedResources[i].ClusterName = cluster.ID, cluster.Name
	}
	for i := range res.SecretRefs {
		res.SecretRefs[i].Cluster, res.SecretRefs[i].ClusterName = cluster.ID, cluster.Name
	}
	res.Entries = append(res.Entries,
		detect.ManagedEntry([]string{"rds.aws.upbound.io"}, nil, 2, 0, false),
		detect.TerraformEntry([]string{demoTerraformState}, nil),
		detect.CloudInventoryEntry("off", demoOperatorIdentity, "", true, nil))
	return res
}

// demoManagedProvisioning is the platform adapter's signed report that it
// provisioned ticketing-service 1.2.0's postgresql in staging through the
// PostgreSQLInstance claim: it puts ticketing-db through the postgresql
// offering. It is the only report on that use, so it raises no conflict.
func demoManagedProvisioning(src *EmbedSource) fleet.RawProvisioning {
	return fleet.RawProvisioning{Service: "ticketing-service", Digest: demoDigest(src, "ticketing-service", "1.2.0"), Kind: "dependency", Name: "postgresql",
		Environment: "staging", Provider: "postgresql", ExternalRef: demoTicketingClaim, Phase: string(evidence.PhaseReady),
		Outcome: string(evidence.Observed), Producer: "platform-adapter", ProducerKeyID: "demo-platform-adapter",
		ObservedAt: demoNow().Add(-30 * time.Minute), Verified: true}
}

// demoOrdersDBEngine is the observed engine each inferred revision of
// orders-db records: the instance moved from postgres 15 to 16.
var demoOrdersDBEngine = map[string]string{"0.0.1": "postgres 15", "0.0.2": "postgres 16"}

// demoOrdersDBSurface is what the cluster showed of orders-db at version:
// the catalog match and no host, since only its object was read.
func demoOrdersDBSurface(version string) contract.InferredSurface {
	return contract.InferredSurface{
		Identity: string(demoOrdersDB), Class: externalcatalog.ClassManagedCloud, AsOf: stamp(demoInferredAsOf(version)),
		Completeness: &contract.SurfaceCompleteness{State: "complete"},
		Catalog:      &contract.InferredCatalogMatch{Origin: externalcatalog.OriginDefault, Digest: externalcatalog.DefaultDigest(), Entry: "aws/rds"},
	}
}

// demoOrdersDBRevisions are orders-db's two inferred contracts as the
// registry mirror reads them. Unsigned like stripe's, so the engine change
// reads unchecked and raises no item.
func demoOrdersDBRevisions() []fleet.RawRevision {
	return []fleet.RawRevision{demoOrdersDBRevision("0.0.1"), demoOrdersDBRevision("0.0.2")}
}

// demoOrdersDBRevision is orders-db's inferred contract at version.
func demoOrdersDBRevision(version string) fleet.RawRevision {
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: orders-db\n  version: " + version + "\n  external: " + string(demoOrdersDB) + "\n" +
		"metadata:\n  " + fleet.ExternalEngineMetadataKey + ": \"" + demoOrdersDBEngine[version] + "\"\n"
	return demoInferredRawRevision(string(demoOrdersDB), version, raw, demoOrdersDBSurface(version))
}
