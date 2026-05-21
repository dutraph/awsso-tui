# awsso

TUI para escolher conta + role do AWS SSO, salvar como profile em `~/.aws/config`, e em seguida escolher um cluster EKS e atualizar o `~/.kube/config`.

Fluxo:

1. `awsso configure` — pergunta start URL, região do SSO e região default do EKS, salva em `~/.config/awsso/config.toml` e espelha um bloco `[sso-session awsso]` em `~/.aws/config`.
2. `awsso` — abre o browser para login SSO (com cache do token compartilhado com a `aws` CLI), mostra TUI para escolher conta → role → cluster EKS, e roda `aws eks update-kubeconfig` no final.

## Instalação

### Opção A — `go install` (mais rápido)

```bash
go install github.com/dutraph/awsso-tui@latest
```

Garanta que `$(go env GOBIN)` (ou `$(go env GOPATH)/bin`) está no `PATH`.

### Opção B — script de instalação

Dentro do seu bootstrap de devops:

```bash
curl -fsSL https://raw.githubusercontent.com/dutraph/awsso-tui/main/install.sh | bash
# ou com prefix custom:
PREFIX="$HOME/.local" curl -fsSL https://raw.githubusercontent.com/dutraph/awsso-tui/main/install.sh | bash
```

### Opção C — build local

```bash
git clone https://github.com/dutraph/awsso-tui.git
cd awsso
make install            # /usr/local/bin/awsso
# ou
PREFIX=$HOME/.local make install
```

## Pré-requisitos

- Go 1.22+ (para build)
- `aws` CLI v2 no `PATH` — usado por `update-kubeconfig` e como exec-auth plugin no `kubectl`
- `kubectl` (opcional, só pra usar o contexto depois)

## Uso

Primeiro uso:

```bash
awsso configure \
  --start-url https://<d-xxxxxx>.awsapps.com/start \
  --sso-region eu-central-1 \
  --eks-region eu-south-2
```

Depois disso, é só rodar `awsso` quando quiser trocar de conta/cluster:

```bash
awsso
```

A TUI:

- `↑/↓` (ou `j/k`) navega
- `/` filtra
- `enter` seleciona
- `q` ou `esc` sai sem fazer nada

Após escolher conta + role, um profile chamado `<account_name>-<role>` é gravado em `~/.aws/config`. Use com:

```bash
export AWS_PROFILE=prod-AdministratorAccess
aws sts get-caller-identity
kubectl get nodes
```

## Como funciona

- O login SSO é o fluxo de **device authorization** padrão (`ssooidc:StartDeviceAuthorization` + polling em `CreateToken`).
- O access token é cacheado em `~/.aws/sso/cache/<sha1(session)>.json` no mesmo formato que `aws sso login` usa — então sessões iniciadas pela `aws` CLI também são reaproveitadas, e vice-versa.
- O profile gravado em `~/.aws/config` referencia um `sso_session`, que é o mecanismo recomendado da AWS hoje. O SDK e a CLI sabem renovar credenciais transparentemente a partir disso.
- Listagem de clusters EKS usa o SDK Go v2 (`eks:ListClusters`) com o profile recém-criado.
- A montagem do `~/.kube/config` é delegada para `aws eks update-kubeconfig`, que produz o bloco `exec` correto (chamando `aws eks get-token`) e faz merge no kubeconfig existente sem destruir contextos.

## Estrutura

```
.
├── main.go
├── internal/
│   ├── config/      # config persistido em ~/.config/awsso/
│   ├── ssoauth/     # device flow + token cache + ListAccounts/Roles
│   ├── awscfg/      # escreve [sso-session] e [profile] em ~/.aws/config
│   ├── eks/         # ListClusters + shell out para update-kubeconfig
│   └── tui/         # 3 telas de seleção em Bubble Tea
├── Makefile
└── install.sh
```
