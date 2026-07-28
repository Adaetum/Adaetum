# Crafty's companion reads only the desired native administrator credential.
# It applies that value through Crafty's supported API and cannot access any
# other application secret.
path "secret/data/apps/games/crafty/admin" {
  capabilities = ["read"]
}
