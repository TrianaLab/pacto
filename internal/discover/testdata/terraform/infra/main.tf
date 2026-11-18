# The account and region of every queue come from the one aws provider.
provider "aws" {
  region              = "eu-west-1"
  allowed_account_ids = ["123456789012"]
}

provider "aws" {
  alias  = "us"
  region = "us-east-1"
}

resource "aws_sqs_queue" "orders" {
  name   = "orders-queue"
  policy = <<-EOF
    { "Statement": [ { "Effect": "Allow" } ] }
  EOF
}

/* resource "aws_sqs_queue" "commented" {
  name = "commented-queue"
} */

resource "aws_sqs_queue" "us_orders" {
  provider = aws.us
  name     = "us-orders"
}

resource "aws_db_instance" "main" {
  identifier = "${var.prefix}-db"
  password   = "tf-hunter2" # never read
}

resource "aws_instance" "web" { # no catalog row lists it
  ami = "ami-123"
}

module "network" {
  source = "./network"
}
