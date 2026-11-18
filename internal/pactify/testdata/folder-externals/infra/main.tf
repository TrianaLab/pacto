resource "aws_db_instance" "main" {
  identifier = "orders-db"
  password   = "tf-hunter2"
}
