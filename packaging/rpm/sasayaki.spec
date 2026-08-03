Name:           sasayaki
Version:        1.0.0
Release:        1%{?dist}
Summary:        Standalone local voice input and speech-to-text

License:        MIT
URL:            https://github.com/iamcheyan/sasayaki
Source0:        %{name}-%{version}.tar.gz

# System-level tools the program needs at runtime. The private Python
# runtime, speech model, and user service are provisioned per-user by
# `sasayaki setup`, so they are deliberately NOT package dependencies.
BuildRequires:  golang >= 1.22
Requires:       python3
Requires:       pulseaudio-utils
Requires:       wl-clipboard
Requires:       wtype
Requires:       xclip
Requires:       xdotool
Requires:       ydotool
Requires:       systemd

%description
Sasayaki records audio, transcribes it locally with an offline SenseVoice
engine, and pastes the result into the focused application. It is a single
standalone binary, independent of any desktop environment; when the Sumika
Shell desktop is present it additionally registers as a first-class module.

%prep
%setup -q

%build
go build -trimpath -ldflags="-s -w" -o sasayaki ./cmd/sasayaki

%install
install -Dm755 sasayaki %{buildroot}%{_bindir}/sasayaki

%files
%{_bindir}/sasayaki

%changelog
* Tue Aug 04 2026 Sasayaki contributors <noreply@example.invalid> - 1.0.0-1
- Initial packaging.
