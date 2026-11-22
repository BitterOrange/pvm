# Mozilla's own build, as the images have it: Ubuntu's firefox package only
# installs the snap.
if [ ! -x "/usr/lib/firefox/firefox" ]; then
  apt_repo mozilla https://packages.mozilla.org/apt/repo-signing-key.gpg 35BAA0B33E9EB396F59CA838C0BA5CE6DC6315A3 https://packages.mozilla.org/apt mozilla
  # priority 1000 lets Mozilla's firefox beat Ubuntu's higher-epoch snap
  # transitional package
  cat > /etc/apt/preferences.d/mozilla <<'PIN'
Package: *
Pin: origin packages.mozilla.org
Pin-Priority: 1000
PIN
  # a VM that has Ubuntu's 1:1snap1 package sees Mozilla's as older
  $apt install -y --allow-downgrades firefox
fi
firefox --version
