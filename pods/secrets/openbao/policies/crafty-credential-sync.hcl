# Crafty itself generates the initial native administrator credential. Its
# companion may promote only that recovery record; it cannot read or alter any
# other application secret.
path "secret/data/apps/games/crafty/admin" {
  capabilities = ["create", "update"]
}
