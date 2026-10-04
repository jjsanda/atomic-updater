/* exec.c — child process spawning.
 *
 * Every command is resolved through PATH (fork+execvp, or "sh -c" for the
 * reboot command) so the test suite can inject fakes.  Probes run in their
 * own process group so a timeout can SIGKILL the whole tree.
 */
#define _POSIX_C_SOURCE 200809L

#include "health.h"

#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static int status_to_code(int status)
{
    if (WIFEXITED(status))
        return WEXITSTATUS(status);
    if (WIFSIGNALED(status))
        return 128 + WTERMSIG(status);
    return AH_PROBE_ERROR;
}

int run_argv(char *const argv[])
{
    pid_t pid = fork();
    if (pid < 0) {
        ah_err("fork: %s", strerror(errno));
        return -1;
    }
    if (pid == 0) {
        execvp(argv[0], argv);
        fprintf(stderr, "atomic-health: exec %s: %s\n", argv[0],
                strerror(errno));
        _exit(127);
    }
    int status;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno != EINTR) {
            ah_err("waitpid: %s", strerror(errno));
            return -1;
        }
    }
    return status_to_code(status);
}

int run_shell(const char *cmd)
{
    char *argv[] = { "sh", "-c", (char *)cmd, NULL };
    return run_argv(argv);
}

int run_probe(const char *path, int timeout_s)
{
    pid_t pid = fork();
    if (pid < 0) {
        ah_err("fork: %s", strerror(errno));
        return AH_PROBE_ERROR;
    }
    if (pid == 0) {
        (void)setpgid(0, 0); /* own process group, so the tree can be killed */
        char *argv[] = { "sh", (char *)path, NULL };
        execvp(argv[0], argv);
        fprintf(stderr, "atomic-health: exec sh %s: %s\n", path,
                strerror(errno));
        _exit(127);
    }
    (void)setpgid(pid, pid); /* mirror in parent; harmless if child won */

    const long poll_ms = 50;
    const long limit_ms = (long)timeout_s * 1000L;
    long waited_ms = 0;
    for (;;) {
        int status;
        pid_t r = waitpid(pid, &status, WNOHANG);
        if (r == pid)
            return status_to_code(status);
        if (r < 0 && errno != EINTR) {
            ah_err("waitpid: %s", strerror(errno));
            return AH_PROBE_ERROR;
        }
        if (waited_ms >= limit_ms) {
            if (kill(-pid, SIGKILL) < 0)
                (void)kill(pid, SIGKILL); /* pgid race fallback */
            while (waitpid(pid, &status, 0) < 0 && errno == EINTR)
                ;
            return AH_PROBE_TIMEOUT;
        }
        struct timespec ts = { 0, poll_ms * 1000000L };
        nanosleep(&ts, NULL);
        waited_ms += poll_ms;
    }
}
