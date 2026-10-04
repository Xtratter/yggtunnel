# R8 rules for YggTunnel.
# Native.* are implemented in libygg.so (JNI names are fixed), keep them as is.
-keepclasseswithmembernames class io.github.xtratter.yggtunnel.Native { native <methods>; }
