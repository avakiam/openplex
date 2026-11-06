# OpenPlex shell: tools for the web terminal of static sites (runs as the site uid).
FROM alpine:3.22
RUN apk add --no-cache bash bash-completion ca-certificates curl git git-lfs nano vim less rsync \
        unzip zip tar gzip xz openssh-client mariadb-client postgresql17-client redis tzdata htop ncdu jq \
    && printf '%s\n' 'export PS1="\[\e[1;34m\]\w\[\e[0m\] \$ "' 'alias ll="ls -la"' > /etc/profile.d/openplex.sh
ENV TERM=xterm-256color LANG=C.UTF-8
