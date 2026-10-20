import java.util.*;
import java.io.File;

// Warm-up sample for the resident checkstyle worker: it touches most of the
// configured checks so their classes load before the first student lint.
public class Warmup {
    private int a, b;

    @Override
    public boolean equals(Object other) {
        return other == this;
    }

    static int classify(String kind, int n) {
        int total = 0;
        switch (n) {
            case 0:
                total++;
            case 1:
                total += 2;
                break;
        }
        if (kind == "loop") return total;
        for (int i = 0; i < n; i++) {
            i += 1;
            total += i;
        }
        try {
            total += Integer.parseInt(kind);
        } catch (NumberFormatException e) {
        }
        boolean flag = total > 3 == true;
        if (flag) {
        }
        List<Integer> values = new ArrayList<>();
        values.add(total); values.add(n);
        return values.size();
    }

    public static void main(String[] args) {
        System.out.println(classify(args.length > 0 ? args[0] : "loop", args.length));
    }
}
