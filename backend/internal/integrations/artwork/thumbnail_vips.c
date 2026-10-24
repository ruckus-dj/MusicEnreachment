#include "thumbnail_vips.h"

#include <vips/vips.h>
#include <glib/gstdio.h>
#include <fcntl.h>
#include <stdio.h>

#ifndef O_BINARY
#define O_BINARY 0
#endif

int artwork_vips_thumbnail(const char *input_path, const char *output_path, char *error, size_t error_size) {
	int output_fd = -1;
	VipsTarget *target = NULL;
	VipsImage *thumbnail = NULL;
	int result = -1;

	/* Go created this private staging file with mode 0600; retain that inode and mode. */
	output_fd = g_open(output_path, O_WRONLY | O_TRUNC | O_BINARY, 0);
	if (output_fd < 0) {
		vips_error("artwork", "unable to open staged output file");
		goto done;
	}
	target = vips_target_new_to_descriptor(output_fd);
	if (target == NULL) {
		g_close(output_fd, NULL);
		output_fd = -1;
		goto done;
	}
	output_fd = -1; /* target owns the descriptor */

	if (vips_thumbnail(input_path, &thumbnail, 2048,
		"height", 2048,
		"size", VIPS_SIZE_DOWN,
		"no_rotate", TRUE,
		"fail_on", VIPS_FAIL_ON_ERROR,
		NULL) == 0) {
		if (vips_pngsave_target(thumbnail, target, NULL) == 0) {
			result = 0;
		}
	}

done:
	if (result != 0 && error != NULL && error_size > 0) {
		const char *message = vips_error_buffer();
		if (message == NULL || message[0] == '\0') {
			message = "libvips thumbnail operation failed";
		}
		snprintf(error, error_size, "%s", message);
	}
	vips_error_clear();
	if (thumbnail != NULL) {
		g_object_unref(thumbnail);
	}
	if (target != NULL) {
		g_object_unref(target);
	}
	if (output_fd >= 0) {
		g_close(output_fd, NULL);
	}
	return result;
}
