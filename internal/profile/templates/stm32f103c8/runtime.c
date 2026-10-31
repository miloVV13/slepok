/* Vendor Reset_Handler calls __libc_init_array. With -nostartfiles, provide
 * the init/fini hooks normally supplied by crti, without a second crt0. */
void _init(void) {}
void _fini(void) {}
