# Workers in several regions with Terraform

Two examples put Stampede workers in several cloud regions, all joining one
Stampede server, so a run can split its load by region:

- [`deploy/terraform/aws`](../../deploy/terraform/aws): one Auto Scaling
  group of Amazon Linux 2023 instances per region.
- [`deploy/terraform/gcp`](../../deploy/terraform/gcp): one regional managed
  instance group of Debian 12 VMs per region.

Each VM, at boot, downloads the Stampede release for its architecture from
GitHub, checks it against the release's checksum file, installs it, and
runs it under systemd as:

```
stampede worker --server <server_address> --region <cloud region> --name <host name> \
  --label cloud=aws|gcp [--label KEY=VALUE ...] [--mtls [--ca-fingerprint sha256:...] | --insecure | --ca /etc/stampede/ca.pem] [--max-vus N]
```

The shared startup script is
[`deploy/terraform/templates/worker-startup.sh.tftpl`](../../deploy/terraform/templates/worker-startup.sh.tftpl).

## Before you start

1. A Stampede server whose **worker port (8081) is reachable** from the
   regions, for example the Helm chart with `service.type=LoadBalancer`.
   Workers only make outbound connections, so the examples create no
   inbound firewall rules.
2. The join token:
   `kubectl -n stampede get secret stampede -o jsonpath='{.data.join-token}' | base64 -d`
3. TLS on the worker port. By default (`mtls = true`) workers enroll with
   the server's built-in CA, which the Helm chart and Docker Compose turn
   on, and the join token never crosses the network. Set
   `ca_fingerprint` to the `ca=sha256:...` value the server logs at start,
   so workers accept only that server. For a certificate from your own CA,
   set `server_ca_pem` instead. `insecure = true` sends the join token and
   scenario secrets in clear text.
4. A published Stampede release (`stampede_version`), since the VMs
   download it from GitHub releases.

## The join token stays out of instance metadata

- AWS: stored as an SSM Parameter Store `SecureString` per region; the
  instance role may only `ssm:GetParameter` that one parameter, and the
  instance reads it with the AWS CLI at boot. IMDSv2 is required.
- GCP: stored in Secret Manager; a dedicated service account has
  `secretAccessor` on that one secret, and the VM reads it through the
  metadata server's access token.

On the VM the token lives in `/etc/stampede/worker.env` (root:stampede,
0640) and reaches the worker as `STAMPEDE_JOIN_TOKEN`, not on the command
line. It is in Terraform state, so keep state in an encrypted, access
controlled backend.

## AWS

```sh
cd deploy/terraform/aws
cp terraform.tfvars.example terraform.tfvars    # edit
export TF_VAR_join_token=...
terraform init && terraform apply
```

| Variable | Default | Meaning |
|---|---|---|
| `server_address` | | `host:port` of the worker port |
| `join_token` | | sensitive |
| `workers_per_region` | | e.g. `{ "us-east-1" = 2, "ap-south-1" = 1 }` |
| `instance_type` / `architecture` | `c7g.large` / `arm64` | must match each other |
| `stampede_version` | | release to install, e.g. `0.1.0` |
| `subnet_ids` | default VPC | per region; set `public_ip = false` behind a NAT gateway |
| `mtls`, `ca_fingerprint` | `true`, `""` | worker mutual TLS with the server's built-in CA |
| `insecure`, `server_ca_pem` | `false`, `""` | instead of mtls: no TLS, or your own CA (these win over `mtls`) |
| `worker_labels`, `max_vus`, `tags`, `name_prefix` | | |

Terraform cannot create providers in a loop, so the example has one
provider alias and module block per supported region: `us-east-1`,
`eu-west-1` and `ap-south-1`. A region with 0 workers creates nothing. To
add a region, copy a provider/module pair in `main.tf` and add the region
to the validation list in `variables.tf`.

## GCP

```sh
cd deploy/terraform/gcp
cp terraform.tfvars.example terraform.tfvars    # edit
export TF_VAR_join_token=...
terraform init && terraform apply
```

| Variable | Default | Meaning |
|---|---|---|
| `project_id` | | |
| `server_address`, `join_token`, `stampede_version` | | as for AWS |
| `workers_per_region` | | e.g. `{ "europe-west1" = 2, "asia-south1" = 1 }`; any region |
| `subnetworks` | | subnetwork per region (`"default"` on the default network) |
| `machine_type` | `e2-standard-2` | |
| `image` | `debian-cloud/debian-12` | needs bash, curl, python3, systemd |
| `public_ip` | `true` | set `false` with Cloud NAT |
| `mtls`, `ca_fingerprint`, `insecure`, `server_ca_pem`, `worker_labels`, `max_vus`, `network`, `name_prefix` | | |

The google provider is not tied to a region, so GCP regions are a plain
map. `worker_labels` are also set as GCE labels, so keep them to lowercase
letters, digits, `-` and `_`.

## Check the workers joined

In the web UI's Workers page, or:

```sh
curl -s -H "Authorization: Bearer $STAMPEDE_TOKEN" https://stampede.example.com/api/v1/workers |
  jq -r '.[] | [.name, .region, .labels.cloud, .status] | @tsv'
```

On a VM: `journalctl -u stampede-worker -f`.

## What was checked

`terraform fmt -check` and `terraform init -backend=false && terraform
validate` pass for both examples (Terraform 1.16.5, AWS provider 6.67, and
the Google provider Terraform picked within `>= 6.0, < 8.0`), and the rendered
startup script passes `bash -n`.
They have **not been applied** to real AWS or GCP accounts, and the startup
script has not run against a published release (none exists yet). The CI
workflow `.github/workflows/k8s.yml` repeats the fmt/validate checks.
