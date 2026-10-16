"""Generate reviewable CloudFormation JSON; never contacts AWS or reads secrets."""
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent


def ref(name): return {"Ref": name}
def sub(value): return {"Fn::Sub": value}
def att(name, attribute): return {"Fn::GetAtt": [name, attribute]}
def resource(kind, **properties): return {"Type": "AWS::" + kind, "Properties": properties}
def imported(name): return {"Fn::ImportValue": sub("${FoundationStackName}-" + name)}
def parameter(kind="String", **kwargs): return {"Type": kind, **kwargs}


def foundation():
    r = {}
    r["VPC"] = resource("EC2::VPC", CidrBlock="10.90.0.0/16", EnableDnsSupport=True, EnableDnsHostnames=True)
    r["IGW"] = resource("EC2::InternetGateway")
    r["AttachGateway"] = resource("EC2::VPCGatewayAttachment", VpcId=ref("VPC"), InternetGatewayId=ref("IGW"))
    r["PublicRoutes"] = resource("EC2::RouteTable", VpcId=ref("VPC"))
    r["InternetRoute"] = resource("EC2::Route", RouteTableId=ref("PublicRoutes"), DestinationCidrBlock="0.0.0.0/0", GatewayId=ref("IGW"))
    r["InternetRoute"]["DependsOn"] = "AttachGateway"
    for i in (1, 2):
        zone = {"Fn::Select": [i - 1, {"Fn::GetAZs": ""}]}
        r[f"Public{i}"] = resource("EC2::Subnet", VpcId=ref("VPC"), CidrBlock=f"10.90.{i}.0/24", AvailabilityZone=zone, MapPublicIpOnLaunch=False)
        r[f"Private{i}"] = resource("EC2::Subnet", VpcId=ref("VPC"), CidrBlock=f"10.90.{i+10}.0/24", AvailabilityZone=zone, MapPublicIpOnLaunch=False)
        r[f"PublicAssociation{i}"] = resource("EC2::SubnetRouteTableAssociation", SubnetId=ref(f"Public{i}"), RouteTableId=ref("PublicRoutes"))
        r[f"EIP{i}"] = resource("EC2::EIP", Domain="vpc")
        r[f"NAT{i}"] = resource("EC2::NatGateway", AllocationId=att(f"EIP{i}", "AllocationId"), SubnetId=ref(f"Public{i}"))
        r[f"NAT{i}"]["DependsOn"] = "AttachGateway"
        r[f"PrivateRoutes{i}"] = resource("EC2::RouteTable", VpcId=ref("VPC"))
        r[f"PrivateRoute{i}"] = resource("EC2::Route", RouteTableId=ref(f"PrivateRoutes{i}"), DestinationCidrBlock="0.0.0.0/0", NatGatewayId=ref(f"NAT{i}"))
        r[f"PrivateAssociation{i}"] = resource("EC2::SubnetRouteTableAssociation", SubnetId=ref(f"Private{i}"), RouteTableId=ref(f"PrivateRoutes{i}"))
    r["TaskSG"] = resource("EC2::SecurityGroup", GroupDescription="Private application tasks; ingress added by application stack", VpcId=ref("VPC"))
    for name, port in (("DatabaseSG", 5432), ("RedisSG", 6379)):
        r[name] = resource("EC2::SecurityGroup", GroupDescription="Only application tasks", VpcId=ref("VPC"), SecurityGroupIngress=[{"IpProtocol": "tcp", "FromPort": port, "ToPort": port, "SourceSecurityGroupId": ref("TaskSG")}], SecurityGroupEgress=[])
    subnets = [ref("Private1"), ref("Private2")]
    r["DatabaseSubnets"] = resource("RDS::DBSubnetGroup", DBSubnetGroupDescription="Private two-zone database", SubnetIds=subnets)
    r["DatabaseCredentials"] = resource("SecretsManager::Secret", Description="Database bootstrap credentials; rotate with coordinated runtime update", GenerateSecretString={"SecretStringTemplate": '{"username":"atladmin"}', "GenerateStringKey": "password", "PasswordLength": 40, "ExcludePunctuation": True})
    r["DatabaseCredentials"].update(DeletionPolicy="Retain", UpdateReplacePolicy="Retain")
    r["Database"] = resource("RDS::DBInstance", Engine="postgres", DBInstanceClass=ref("DatabaseClass"), AllocatedStorage="20", MaxAllocatedStorage=100, StorageType="gp3", StorageEncrypted=True, MultiAZ=True, PubliclyAccessible=False, DBName="across_db", MasterUsername="atladmin", MasterUserPassword=sub("{{resolve:secretsmanager:${DatabaseCredentials}:SecretString:password}}"), DBSubnetGroupName=ref("DatabaseSubnets"), VPCSecurityGroups=[ref("DatabaseSG")], BackupRetentionPeriod=7, DeletionProtection=True, CopyTagsToSnapshot=True, AutoMinorVersionUpgrade=True)
    r["Database"].update(DeletionPolicy="Snapshot", UpdateReplacePolicy="Snapshot")
    r["RedisPassword"] = resource("SecretsManager::Secret", Description="Generated cache authentication", GenerateSecretString={"PasswordLength": 32, "ExcludePunctuation": True})
    r["RedisPassword"].update(DeletionPolicy="Retain", UpdateReplacePolicy="Retain")
    r["RedisSubnets"] = resource("ElastiCache::SubnetGroup", Description="Private cache", SubnetIds=subnets)
    r["Redis"] = resource("ElastiCache::ReplicationGroup", ReplicationGroupDescription="Atl xpres shared cache", Engine="redis", EngineVersion="7.1", CacheNodeType=ref("CacheClass"), NumCacheClusters=2, AutomaticFailoverEnabled=True, MultiAZEnabled=True, AtRestEncryptionEnabled=True, TransitEncryptionEnabled=True, AuthToken=sub("{{resolve:secretsmanager:${RedisPassword}:SecretString}}"), CacheSubnetGroupName=ref("RedisSubnets"), SecurityGroupIds=[ref("RedisSG")], SnapshotRetentionLimit=7)
    r["Redis"].update(DeletionPolicy="Snapshot", UpdateReplacePolicy="Snapshot")
    r["Images"] = resource("ECR::Repository", ImageTagMutability="IMMUTABLE", ImageScanningConfiguration={"ScanOnPush": True}, EmptyOnDelete=False)
    r["Images"].update(DeletionPolicy="Retain", UpdateReplacePolicy="Retain")
    r["BuildRole"] = resource("IAM::Role", AssumeRolePolicyDocument={"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Federated": ref("GitHubOidcProviderArn")}, "Action": "sts:AssumeRoleWithWebIdentity", "Condition": {"StringEquals": {"token.actions.githubusercontent.com:aud": "sts.amazonaws.com", "token.actions.githubusercontent.com:sub": "repo:3my1234/across-backend-api:ref:refs/heads/main"}}}]}, Policies=[{"PolicyName": "PushThisRepository", "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "ecr:GetAuthorizationToken", "Resource": "*"}, {"Effect": "Allow", "Action": ["ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart", "ecr:CompleteLayerUpload", "ecr:PutImage"], "Resource": att("Images", "Arn")}]}}])
    r["BuildRole"]["Condition"] = "HasGitHubProvider"
    r["Cluster"] = resource("ECS::Cluster", ClusterSettings=[{"Name": "containerInsights", "Value": "enabled"}])
    outputs = {name: {"Value": ref(name), "Export": {"Name": sub("${AWS::StackName}-" + name)}} for name in ("VPC", "Public1", "Public2", "Private1", "Private2", "TaskSG", "Cluster")}
    outputs.update({
        "DatabaseHost": {"Value": att("Database", "Endpoint.Address")},
        "DatabaseSecretArn": {"Value": ref("DatabaseCredentials")},
        "RedisHost": {"Value": att("Redis", "PrimaryEndPoint.Address")},
        "RedisSecretArn": {"Value": ref("RedisPassword")},
        "ImageRepository": {"Value": att("Images", "RepositoryUri")},
        "BuildRoleArn": {"Condition": "HasGitHubProvider", "Value": att("BuildRole", "Arn")},
    })
    return {"AWSTemplateFormatVersion": "2010-09-09", "Description": "Atl xpres private AWS foundation. Creates billable resources; no DNS changes or live traffic cutover.", "Conditions": {"HasGitHubProvider": {"Fn::Not": [{"Fn::Equals": [ref("GitHubOidcProviderArn"), ""]}]}}, "Parameters": {"GitHubOidcProviderArn": parameter(Default="", Description="Existing GitHub OIDC provider ARN; leave blank to omit image publishing role"), "DatabaseClass": parameter(Default="db.t4g.small", AllowedValues=["db.t4g.small", "db.t4g.medium", "db.m7g.large"]), "CacheClass": parameter(Default="cache.t4g.small", AllowedValues=["cache.t4g.small", "cache.t4g.medium", "cache.m7g.large"])}, "Resources": r, "Outputs": outputs}


SECRET_KEYS = ["DATABASE_URL", "MIGRATION_DATABASE_URL", "REDIS_URL", "JWT_SECRET", "PRIVY_APP_ID", "PRIVY_APP_SECRET", "PRIVY_VERIFICATION_KEY", "FLUTTERWAVE_SECRET_KEY", "FLUTTERWAVE_WEBHOOK_SECRET", "SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "WEB_PUSH_VAPID_PUBLIC_KEY", "WEB_PUSH_VAPID_PRIVATE_KEY"]


def application():
    r = {}
    r["Logs"] = resource("Logs::LogGroup", RetentionInDays=14)
    r["ExecutionRole"] = resource("IAM::Role", AssumeRolePolicyDocument={"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Principal": {"Service": "ecs-tasks.amazonaws.com"}, "Action": "sts:AssumeRole"}]}, ManagedPolicyArns=[sub("arn:${AWS::Partition}:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy")], Policies=[{"PolicyName": "RuntimeSecretOnly", "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "secretsmanager:GetSecretValue", "Resource": ref("RuntimeSecretArn")}]}}])
    r["LoadBalancerSG"] = resource("EC2::SecurityGroup", GroupDescription="HTTPS ingress", VpcId=imported("VPC"), SecurityGroupIngress=[{"IpProtocol": "tcp", "FromPort": 443, "ToPort": 443, "CidrIp": "0.0.0.0/0"}])
    r["TaskIngress"] = resource("EC2::SecurityGroupIngress", GroupId=imported("TaskSG"), IpProtocol="tcp", FromPort=8080, ToPort=8080, SourceSecurityGroupId=ref("LoadBalancerSG"))
    r["LoadBalancer"] = resource("ElasticLoadBalancingV2::LoadBalancer", Type="application", Scheme="internet-facing", Subnets=[imported("Public1"), imported("Public2")], SecurityGroups=[ref("LoadBalancerSG")], LoadBalancerAttributes=[{"Key": "routing.http.drop_invalid_header_fields.enabled", "Value": "true"}, {"Key": "idle_timeout.timeout_seconds", "Value": "65"}])
    r["TargetGroup"] = resource("ElasticLoadBalancingV2::TargetGroup", VpcId=imported("VPC"), Port=8080, Protocol="HTTP", TargetType="ip", HealthCheckPath="/api/v1/ready", HealthCheckIntervalSeconds=10, HealthCheckTimeoutSeconds=3, HealthyThresholdCount=2, UnhealthyThresholdCount=2, Matcher={"HttpCode": "200"}, TargetGroupAttributes=[{"Key": "deregistration_delay.timeout_seconds", "Value": "70"}])
    r["Listener"] = resource("ElasticLoadBalancingV2::Listener", LoadBalancerArn=ref("LoadBalancer"), Port=443, Protocol="HTTPS", SslPolicy="ELBSecurityPolicy-TLS13-1-2-2021-06", Certificates=[{"CertificateArn": ref("CertificateArn")}], DefaultActions=[{"Type": "forward", "TargetGroupArn": ref("TargetGroup")}])
    r["WebACL"] = resource("WAFv2::WebACL", Scope="REGIONAL", DefaultAction={"Allow": {}}, VisibilityConfig={"CloudWatchMetricsEnabled": True, "MetricName": "atl-api-waf", "SampledRequestsEnabled": False}, Rules=[{"Name": "ManagedCommon", "Priority": 0, "OverrideAction": {"None": {}}, "Statement": {"ManagedRuleGroupStatement": {"VendorName": "AWS", "Name": "AWSManagedRulesCommonRuleSet", "RuleActionOverrides": [{"Name": "SizeRestrictions_BODY", "ActionToUse": {"Count": {}}}]}}, "VisibilityConfig": {"CloudWatchMetricsEnabled": True, "MetricName": "atl-common", "SampledRequestsEnabled": False}}])
    r["WebACLAssociation"] = resource("WAFv2::WebACLAssociation", ResourceArn=ref("LoadBalancer"), WebACLArn=att("WebACL", "Arn"))
    env = {"APP_ENV": "production", "HTTP_ADDR": ":8080", "RUN_MIGRATIONS": "false", "RUN_INLINE_WORKERS": "false", "REDIS_OPTIONAL": "false", "DB_MAX_CONNS": "4", "DB_MIN_CONNS": "1", "HTTP_MAX_IN_FLIGHT": "64", "AWS_REGION": ref("MediaRegion"), "S3_BUCKET_NAME": ref("MediaBucket"), "SMTP_PORT": "587", "SMTP_FROM_EMAIL": "welcome@atlxpres.com", "ALLOWED_ORIGINS": "https://atlxpres.com,https://admin.atlxpres.com,https://provider.atlxpres.com"}
    common = {"Image": ref("ImageUri"), "Essential": True, "StopTimeout": 90, "LogConfiguration": {"LogDriver": "awslogs", "Options": {"awslogs-group": ref("Logs"), "awslogs-region": ref("AWS::Region"), "awslogs-stream-prefix": "app"}}, "Secrets": [{"Name": key, "ValueFrom": sub("${RuntimeSecretArn}:" + key + "::")} for key in SECRET_KEYS], "Environment": [{"Name": key, "Value": value} for key, value in env.items()]}
    for name, cpu, memory, command in (("ApiTask", "1024", "2048", "/app/across-api"), ("WorkerTask", "512", "1024", "/app/across-worker"), ("MigrationTask", "512", "1024", "/app/across-migrate")):
        container = {**common, "Name": "app", "Command": [command]}
        if name != "MigrationTask":
            container["PortMappings"] = [{"ContainerPort": 8080, "Protocol": "tcp"}]
            container["HealthCheck"] = {"Command": ["CMD-SHELL", "curl -fsS --max-time 3 http://localhost:8080/api/v1/health || exit 1"], "Interval": 15, "Timeout": 4, "Retries": 3, "StartPeriod": 30}
        r[name] = resource("ECS::TaskDefinition", Cpu=cpu, Memory=memory, NetworkMode="awsvpc", RequiresCompatibilities=["FARGATE"], RuntimePlatform={"CpuArchitecture": "X86_64", "OperatingSystemFamily": "LINUX"}, ExecutionRoleArn=att("ExecutionRole", "Arn"), ContainerDefinitions=[container])
    network = {"AwsvpcConfiguration": {"AssignPublicIp": "DISABLED", "Subnets": [imported("Private1"), imported("Private2")], "SecurityGroups": [imported("TaskSG")]}}
    for name, definition, count in (("ApiService", "ApiTask", 2), ("WorkerService", "WorkerTask", 1)):
        properties = {"Cluster": imported("Cluster"), "TaskDefinition": ref(definition), "LaunchType": "FARGATE", "PlatformVersion": "1.4.0", "DesiredCount": {"Fn::If": ["Enabled", count, 0]}, "NetworkConfiguration": network, "DeploymentConfiguration": {"MaximumPercent": 150 if name == "ApiService" else 200, "MinimumHealthyPercent": 100, "DeploymentCircuitBreaker": {"Enable": True, "Rollback": True}}}
        if name == "ApiService": properties.update(HealthCheckGracePeriodSeconds=60, AvailabilityZoneRebalancing="ENABLED", LoadBalancers=[{"ContainerName": "app", "ContainerPort": 8080, "TargetGroupArn": ref("TargetGroup")}])
        r[name] = resource("ECS::Service", **properties)
        if name == "ApiService": r[name]["DependsOn"] = ["Listener", "TaskIngress"]
    r["ScaleTarget"] = resource("ApplicationAutoScaling::ScalableTarget", MinCapacity=2, MaxCapacity=ref("MaxApiTasks"), ResourceId={"Fn::Join": ["", ["service/", imported("Cluster"), "/", att("ApiService", "Name")]]}, ScalableDimension="ecs:service:DesiredCount", ServiceNamespace="ecs", RoleARN=sub("arn:${AWS::Partition}:iam::${AWS::AccountId}:role/aws-service-role/ecs.application-autoscaling.amazonaws.com/AWSServiceRoleForApplicationAutoScaling_ECSService"))
    r["ScaleTarget"]["Condition"] = "Enabled"
    r["CPUScaling"] = resource("ApplicationAutoScaling::ScalingPolicy", PolicyName="api-cpu-55", PolicyType="TargetTrackingScaling", ScalingTargetId=ref("ScaleTarget"), TargetTrackingScalingPolicyConfiguration={"TargetValue": 55, "ScaleOutCooldown": 60, "ScaleInCooldown": 300, "PredefinedMetricSpecification": {"PredefinedMetricType": "ECSServiceAverageCPUUtilization"}})
    r["CPUScaling"]["Condition"] = "Enabled"
    r["Alerts"] = resource("SNS::Topic")
    r["ApiErrors"] = resource("CloudWatch::Alarm", AlarmDescription="API target 5xx; investigate before increasing replicas", Namespace="AWS/ApplicationELB", MetricName="HTTPCode_Target_5XX_Count", Dimensions=[{"Name": "LoadBalancer", "Value": att("LoadBalancer", "LoadBalancerFullName")}], Statistic="Sum", Period=60, EvaluationPeriods=2, Threshold=10, ComparisonOperator="GreaterThanOrEqualToThreshold", TreatMissingData="notBreaching", AlarmActions=[ref("Alerts")])
    return {"AWSTemplateFormatVersion": "2010-09-09", "Description": "Atl xpres pay-as-you-go ALB and private ECS Fargate. Starts paused; no DNS changes.", "Parameters": {
        "FoundationStackName": parameter(), "ImageUri": parameter(AllowedPattern=r"[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/.+@sha256:[a-f0-9]{64}"),
        "RuntimeSecretArn": parameter(AllowedPattern=r"arn:aws:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:.+"),
        "CertificateArn": parameter(AllowedPattern=r"arn:aws:acm:[a-z0-9-]+:[0-9]{12}:certificate/.+"),
        "MediaBucket": parameter(), "MediaRegion": parameter(Default="eu-north-1"),
        "EnableService": parameter(Default="false", AllowedValues=["false", "true"]),
        "MaxApiTasks": parameter("Number", Default=10, MinValue=2, MaxValue=10),
    }, "Conditions": {"Enabled": {"Fn::Equals": [ref("EnableService"), "true"]}}, "Resources": r,
        "Outputs": {"LoadBalancerDNS": {"Value": att("LoadBalancer", "DNSName")}, "ApiService": {"Value": att("ApiService", "Name")}, "MigrationTask": {"Value": ref("MigrationTask")}, "AlertTopicArn": {"Value": ref("Alerts")}, "RuntimeSecretKeys": {"Value": ",".join(SECRET_KEYS)}}}


if __name__ == "__main__":
    for name, template in (("foundation", foundation()), ("application", application())):
        (OUT / f"{name}.json").write_text(json.dumps(template, indent=2) + "\n", encoding="utf-8")
        print(f"Generated {name}.json (no AWS actions)")
