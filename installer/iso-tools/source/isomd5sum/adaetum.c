#include <stdio.h>
#include <string.h>
#include "libimplantisomd5.h"
#include "libcheckisomd5.h"
int main(int argc,char **argv) {
 if(argc != 3) { fprintf(stderr,"usage: iso-checksum implant|check image.iso\n"); return 2; }
 if(!strcmp(argv[1],"implant")) { char *error = NULL; int r=implantISOFile(argv[2],0,0,1,&error); if(r && error) fprintf(stderr,"%s\n",error); return r ? 1 : 0; }
 if(!strcmp(argv[1],"check")) { int r=mediaCheckFile(argv[2],NULL,NULL); if (r != ISOMD5SUM_CHECK_PASSED) fprintf(stderr,"ISO checksum verification failed (%d)\n",r); else printf("ISO checksum verified.\n"); return r == ISOMD5SUM_CHECK_PASSED ? 0 : 1; }
 return 2;
}
