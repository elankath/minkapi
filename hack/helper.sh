function getGoBin() {
  local goBin goPath
  goBin=$(go env GOBIN)
  if [[ -n "$goBin" ]]; then
    echo "$goBin"
  fi
  goPath=$(go env GOPATH)
  if 
  echo "$goPath"
}