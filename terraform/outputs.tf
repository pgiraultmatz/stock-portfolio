output "ec2_public_ip" {
  value       = aws_instance.main.public_ip
  description = "EC2 public IP — use for SSH and deploy script"
}

output "ec2_public_dns" {
  value       = aws_instance.main.public_dns
  description = "EC2 public DNS"
}

output "dynamodb_table" {
  value       = aws_dynamodb_table.main.name
  description = "DynamoDB table name"
}
