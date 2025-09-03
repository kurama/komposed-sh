variable "crds" {
  type = map(string)
  default = {
    "komposed.sh" = "https://raw.githubusercontent.com/kurama/komposed-sh/refs/heads/main/operator/dist/install.yaml"
  }
}
