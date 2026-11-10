<?php

/**
 * OpenPlex integration for Roundcube (installed and configured by openplexd).
 *
 * - One-click login: the panel signs a short-lived, single-use ticket for a
 *   mailbox (HMAC-SHA256 with a key shared with this plugin). The session
 *   then talks to Dovecot and Postfix as "<mailbox>*<master>", a Dovecot
 *   master user that only works from the webmail container's network.
 * - Captcha on the login form (Cloudflare Turnstile, hCaptcha or Google
 *   reCAPTCHA v2), verified server-side before the password is checked.
 */
class openplex extends rcube_plugin
{
    public $task = 'login|logout|mail|settings|addressbook';

    private const PROVIDERS = [
        'turnstile' => [
            'script' => 'https://challenges.cloudflare.com/turnstile/v0/api.js',
            'class' => 'cf-turnstile',
            'field' => 'cf-turnstile-response',
            'verify' => 'https://challenges.cloudflare.com/turnstile/v0/siteverify',
        ],
        'hcaptcha' => [
            'script' => 'https://js.hcaptcha.com/1/api.js',
            'class' => 'h-captcha',
            'field' => 'h-captcha-response',
            'verify' => 'https://api.hcaptcha.com/siteverify',
        ],
        'recaptcha' => [
            'script' => 'https://www.google.com/recaptcha/api.js',
            'class' => 'g-recaptcha',
            'field' => 'g-recaptcha-response',
            'verify' => 'https://www.google.com/recaptcha/api/siteverify',
        ],
    ];

    /** @var bool set while a ticket login is being performed */
    private $ticket = false;

    public function init()
    {
        $this->add_hook('startup', [$this, 'startup']);
        $this->add_hook('authenticate', [$this, 'authenticate']);
        $this->add_hook('login_after', [$this, 'login_after']);
        $this->add_hook('storage_connect', [$this, 'storage_connect']);
        $this->add_hook('smtp_connect', [$this, 'smtp_connect']);
        $this->add_hook('template_object_loginform', [$this, 'loginform']);
    }

    public function startup($args)
    {
        if ($args['task'] == 'login' && isset($_GET['_opx'])) {
            $args['action'] = 'login';
        }
        return $args;
    }

    public function authenticate($args)
    {
        $rcmail = rcmail::get_instance();
        if (isset($_GET['_opx'])) {
            $user = $this->check_ticket((string) $_GET['_opx']);
            if ($user === null) {
                $args['abort'] = true;
                $args['error'] = $this->text('expired', 'This webmail link has expired. Open the webmail again from the panel.');
                return $args;
            }
            $rcmail->kill_session(); // another mailbox may be signed in
            $this->ticket = true;
            $args['user'] = $user;
            $args['pass'] = bin2hex(random_bytes(16)); // never used: the master password is
            $args['valid'] = true;
            $args['cookiecheck'] = false;
            return $args;
        }
        $captcha = $this->captcha();
        if ($captcha !== null && !$this->verify_captcha($captcha)) {
            $args['abort'] = true;
            $args['error'] = $this->text('notrobot', 'Please confirm that you are not a robot.');
        }
        return $args;
    }

    public function login_after($args)
    {
        if ($this->ticket) {
            $_SESSION['openplex_ticket'] = true;
        }
        return $args;
    }

    private function master_session()
    {
        return $this->ticket || !empty($_SESSION['openplex_ticket']);
    }

    public function storage_connect($args)
    {
        if ($this->master_session()) {
            $cfg = rcmail::get_instance()->config;
            $args['user'] = $args['user'] . '*' . $cfg->get('openplex_master');
            $args['pass'] = $cfg->get('openplex_master_password');
        }
        return $args;
    }

    public function smtp_connect($args)
    {
        if ($this->master_session()) {
            $rcmail = rcmail::get_instance();
            $args['smtp_user'] = $rcmail->get_user_name() . '*' . $rcmail->config->get('openplex_master');
            $args['smtp_pass'] = $rcmail->config->get('openplex_master_password');
        }
        return $args;
    }

    public function loginform($args)
    {
        $captcha = $this->captcha();
        if ($captcha === null) {
            return $args;
        }
        $p = self::PROVIDERS[$captcha['provider']];
        $lang = substr((string) ($_SESSION['language'] ?? ''), 0, 2);
        $src = $p['script'] . ($lang ? '?hl=' . rawurlencode($lang) : '');
        $rcmail = rcmail::get_instance();
        $rcmail->output->add_header(html::tag('script', ['src' => $src, 'async' => 'async', 'defer' => 'defer']));
        $widget = html::div(['class' => $p['class'], 'data-sitekey' => $captcha['site_key'], 'style' => 'margin:12px auto;display:flex;justify-content:center']);
        $args['content'] = str_replace('</tbody></table>', '</tbody></table>' . $widget, $args['content'], $count);
        if (!$count) {
            $args['content'] .= $widget;
        }
        return $args;
    }

    /** @return array|null the configured captcha, when it protects the webmail */
    private function captcha()
    {
        $c = rcmail::get_instance()->config->get('openplex_captcha');
        if (!is_array($c) || empty($c['site_key']) || empty($c['secret']) || !isset(self::PROVIDERS[$c['provider'] ?? ''])) {
            return null;
        }
        return $c;
    }

    private function verify_captcha(array $c)
    {
        $p = self::PROVIDERS[$c['provider']];
        $token = rcube_utils::get_input_string($p['field'], rcube_utils::INPUT_POST);
        if ($token === '' || strlen($token) > 4096) {
            return false;
        }
        $body = http_build_query(['secret' => $c['secret'], 'response' => $token, 'remoteip' => rcube_utils::remote_addr()]);
        $ctx = stream_context_create(['http' => [
            'method' => 'POST',
            'header' => "Content-Type: application/x-www-form-urlencoded\r\n",
            'content' => $body,
            'timeout' => 10,
            'ignore_errors' => true,
        ]]);
        $res = @file_get_contents($p['verify'], false, $ctx);
        if ($res === false) {
            rcube::raise_error(['code' => 500, 'message' => 'openplex: captcha verification unreachable'], true, false);
            return false;
        }
        $data = json_decode($res, true);
        return is_array($data) && !empty($data['success']);
    }

    /** @return string|null the mailbox a valid, unused ticket is for */
    private function check_ticket($ticket)
    {
        $key = (string) rcmail::get_instance()->config->get('openplex_sso_key');
        $parts = explode('.', $ticket);
        if ($key === '' || count($parts) != 2 || strlen($ticket) > 1024) {
            return null;
        }
        $mac = hash_hmac('sha256', $parts[0], hex2bin($key), true);
        if (!hash_equals(rtrim(strtr(base64_encode($mac), '+/', '-_'), '='), $parts[1])) {
            return null;
        }
        $data = json_decode(base64_decode(strtr($parts[0], '-_', '+/')), true);
        if (!is_array($data) || !isset($data['u'], $data['e'], $data['n']) || !is_string($data['u'])
            || !is_int($data['e']) || $data['e'] < time() || !preg_match('/^[a-f0-9]{32}$/', (string) $data['n'])
        ) {
            return null;
        }
        // Single use: remember the nonce until the ticket would have expired anyway.
        $dir = rtrim((string) rcmail::get_instance()->config->get('temp_dir', sys_get_temp_dir()), '/') . '/openplex-tickets';
        if (!is_dir($dir) && !@mkdir($dir, 0700) && !is_dir($dir)) {
            return null;
        }
        foreach (glob($dir . '/*') ?: [] as $f) {
            if (@filemtime($f) < time() - 600) {
                @unlink($f);
            }
        }
        $fh = @fopen($dir . '/' . $data['n'], 'x');
        if ($fh === false) {
            return null;
        }
        fclose($fh);
        return strtolower($data['u']);
    }

    private function text($key, $fallback)
    {
        $this->add_texts('localization/');
        $t = $this->gettext($key);
        return ($t && $t != '[' . $key . ']') ? $t : $fallback;
    }
}
